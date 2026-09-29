package cli

import (
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/buildinfo"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func newVersionCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the gwork version",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			v := versionInfo{
				Version: buildinfo.Version,
				Commit:  buildinfo.Commit,
				Date:    buildinfo.Date,
				Go:      runtime.Version(),
				OS:      runtime.GOOS,
				Arch:    runtime.GOARCH,
			}
			return a.Print(v, func(w io.Writer) error {
				_, err := fmt.Fprintln(w, buildinfo.String())
				return err
			})
		},
	}
}
