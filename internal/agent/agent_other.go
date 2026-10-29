//go:build !unix

package agent

import (
	"errors"
	"net"
	"os"
	"os/exec"
)

// The agent is for macOS (see agent.go); it needs a Unix socket that
// carries files.
var errUnix = errors.New("the paneterm agent runs on Unix only")

const Needed = false

func Path() (string, error)                                           { return "", errUnix }
func Listen(path string) (*net.UnixListener, error)                   { return nil, errUnix }
func Serve(l *net.UnixListener, command func() *exec.Cmd) error       { return errUnix }
func Relay(path string, env []string, in, out, errOut *os.File) error { return errUnix }
