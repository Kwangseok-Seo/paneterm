//go:build !unix

package session

import (
	"errors"
	"io"
)

// Run is unavailable here: paneterm-host needs a Unix PTY.
func Run(in io.Reader, out io.Writer) error {
	return errors.New("paneterm-host runs on macOS and Linux only")
}
