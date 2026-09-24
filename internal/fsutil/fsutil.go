// Package fsutil holds small filesystem helpers shared by the CLI and the
// workspace packages, such as writing downloaded content atomically.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FileMode is the permission of files written by WriteFile. Downloaded
// content (mail attachments, Drive files) is company data, so it is only
// readable by the owner.
const FileMode fs.FileMode = 0o600

// ErrExists is returned when the destination exists and overwriting was
// not requested.
var ErrExists = errors.New("output file already exists")

// CheckDest validates a destination path before any work is done: it fails
// when path is a directory, or when it exists and force is false (wrapping
// ErrExists). A missing file is fine.
func CheckDest(path string, force bool) error {
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("check output %s: %w", path, err)
	case st.IsDir():
		return fmt.Errorf("output %s is a directory; pass a file path", path)
	case !force:
		return existsError(path)
	}
	return nil
}

func existsError(path string) error {
	return fmt.Errorf("%w: %s (use --force to overwrite)", ErrExists, path)
}

// WriteFile copies r to path atomically and returns the number of bytes
// written. The data goes to a temporary file in the same directory, which
// is fsynced and then moved into place, so readers never observe a partial
// file and a failure never leaves one behind. The file gets mode FileMode.
//
// Without force an existing path is never replaced: the check is repeated
// right before the final move (an atomic hard link where the filesystem
// supports it), so a file created while r was being read is kept.
func WriteFile(path string, r io.Reader, force bool) (n int64, err error) {
	if err := CheckDest(path, force); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".gwork-*.part")
	if err != nil {
		return 0, fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		// After a successful rename tmpName no longer exists; after a
		// successful link it must be removed anyway.
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(FileMode); err != nil {
		return 0, fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if n, err = io.Copy(tmp, r); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	closed = true
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	if err := commit(tmpName, path, force); err != nil {
		return 0, err
	}
	return n, nil
}

// commit moves the finished temporary file to path.
func commit(tmpName, path string, force bool) error {
	if err := CheckDest(path, force); err != nil {
		return err
	}
	if !force {
		// A hard link fails atomically when path exists. Fall back to
		// rename (after the check above) on filesystems without links.
		lerr := os.Link(tmpName, path)
		if lerr == nil {
			return nil
		}
		if errors.Is(lerr, fs.ErrExist) {
			return existsError(path)
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}
