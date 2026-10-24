//go:build !unix && !windows

package session

import (
	"errors"
	"io"
)

// Run is unavailable here: paneterm-host needs a Unix PTY or a Windows
// pseudo console.
func Run(in io.Reader, out io.Writer) error {
	return errors.New("paneterm-host runs on macOS, Linux, and Windows only")
}
