//go:build unix

package session

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Run waits for "open", starts the user's login shell on a PTY, and pumps
// bytes both ways until the shell exits or Chrome closes the port.
func Run(in io.Reader, out io.Writer) error {
	s := &sender{w: out}
	open, err := readOpen(in, s)
	if open == nil {
		return err
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

	resize := func(cols, rows uint16) error {
		return pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
	}
	disconnected := make(chan error, 1)
	go func() { disconnected <- pumpInput(in, ptmx, resize, s) }()

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
	cmd.Env = shellEnv("TERM=xterm-256color", "COLORTERM=truecolor")
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
