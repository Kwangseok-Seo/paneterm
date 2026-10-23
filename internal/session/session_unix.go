//go:build unix

package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/Kwangseok-Seo/paneterm/internal/nm"
)

// chunk caps the PTY bytes per "out" message. Base64 grows it by a third,
// which stays far under nm.MaxOutgoing.
const chunk = 32 * 1024

// drain is how long an exited shell's last output gets to reach the panel.
// The PTY normally closes at once; a background job still holding it would
// otherwise hold back the exit forever.
const drain = 100 * time.Millisecond

// sender serializes writes to Chrome, which come from more than one
// goroutine.
type sender struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *sender) send(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nm.Write(s.w, m)
}

// fail reports err to the panel and returns it.
func (s *sender) fail(err error) error {
	s.send(Message{T: "error", Msg: err.Error()})
	return err
}

// Run waits for "open", starts the user's login shell on a PTY, and pumps
// bytes both ways until the shell exits or Chrome closes the port.
func Run(in io.Reader, out io.Writer) error {
	s := &sender{w: out}

	var open Message
	if err := nm.Read(in, &open); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return s.fail(err)
	}
	if open.T != "open" {
		return s.fail(fmt.Errorf(`first message must be "open", got %q`, open.T))
	}

	cmd := shellCommand()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: open.Cols, Rows: open.Rows})
	if err != nil {
		return s.fail(err)
	}
	defer ptmx.Close()

	outputDone := make(chan struct{})
	go func() {
		pumpOutput(ptmx, s)
		close(outputDone)
	}()

	exited := make(chan int, 1)
	go func() {
		cmd.Wait() // its error only restates the status exitCode reads
		exited <- exitCode(cmd.ProcessState)
	}()

	disconnected := make(chan error, 1)
	go func() { disconnected <- pumpInput(in, ptmx, s) }()

	select {
	case code := <-exited:
		select {
		case <-outputDone:
		case <-time.After(drain):
		}
		return s.send(Message{T: "exit", Code: &code})
	case err := <-disconnected:
		// Hang up the shell, as closing a terminal window does. Closing the
		// PTY can't be relied on for that while the process lives: creack/pty
		// leaves the fd in blocking mode, so Close puts off the real close(2)
		// until pumpOutput's pending Read returns. Process.Signal won't signal
		// a shell that Wait has reaped (on macOS a window of a few
		// instructions remains).
		cmd.Process.Signal(syscall.SIGHUP)
		if errors.Is(err, io.EOF) {
			return nil
		}
		return s.fail(err)
	}
}

// exitCode reports a shell killed by a signal as 128+signal, the way shells
// report their own children.
func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

func shellCommand() *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir, _ = os.UserHomeDir()
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	// A shell without a locale treats input as ASCII and mangles anything
	// else. Terminal.app sets one on startup for the same reason; apps
	// started from the Dock, Chrome included, may have none to pass on.
	if os.Getenv("LANG") == "" && os.Getenv("LC_ALL") == "" && os.Getenv("LC_CTYPE") == "" {
		cmd.Env = append(cmd.Env, "LANG="+fallbackLocale())
	}
	return cmd
}

// fallbackLocale names a UTF-8 locale that exists without setup: macOS
// ships en_US.UTF-8, while minimal Linux systems may have only glibc's
// built-in C.UTF-8.
func fallbackLocale() string {
	if runtime.GOOS == "darwin" {
		return "en_US.UTF-8"
	}
	return "C.UTF-8"
}

// pumpOutput forwards PTY output until the PTY closes, which happens once
// the shell and everything it left holding the terminal are gone.
func pumpOutput(ptmx *os.File, s *sender) {
	buf := make([]byte, chunk)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			if s.send(Message{T: "out", D: buf[:n]}) != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// pumpInput applies panel messages to the PTY until Chrome closes stdin.
func pumpInput(in io.Reader, ptmx *os.File, s *sender) error {
	for {
		var m Message
		if err := nm.Read(in, &m); err != nil {
			return err
		}
		switch m.T {
		case "in":
			if _, err := ptmx.Write(m.D); err != nil {
				return err
			}
		case "resize":
			if err := pty.Setsize(ptmx, &pty.Winsize{Cols: m.Cols, Rows: m.Rows}); err != nil {
				return err
			}
		default:
			// The extension and the host are updated separately, so a newer
			// panel may send what this host doesn't know. Say so; keep the
			// shell.
			s.send(Message{T: "error", Msg: fmt.Sprintf("unknown message type %q", m.T)})
		}
	}
}
