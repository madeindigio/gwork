package mcpserver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// decodeBase64Content decodes attachment content in the standard or URL
// alphabet, padded or not, ignoring whitespace, refusing results larger than
// limit bytes. Its errors never include the content.
func decodeBase64Content(s string, limit int64) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	if int64(base64.RawStdEncoding.DecodedLen(len(s))) > limit {
		return nil, fmt.Errorf("content_base64 decodes to more than %d bytes", limit)
	}
	enc := base64.RawStdEncoding
	if strings.ContainsAny(s, "-_") {
		enc = base64.RawURLEncoding
	}
	b, err := enc.DecodeString(s)
	if err != nil {
		return nil, errors.New("content_base64 is not valid base64")
	}
	return b, nil
}
