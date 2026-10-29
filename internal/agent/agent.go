//go:build unix

// Package agent runs sessions outside the process tree Chrome starts. On
// macOS, Chrome quarantines every file that it or a process under it
// creates, and Gatekeeper then refuses to run programs built in the shell
// (docs/adr/0002). The host Chrome starts is a relay instead: it hands
// Chrome's pipes to an agent that launchd keeps running, and the agent
// starts the session on them.
//
//	relay → agent  the environment, NUL-separated, as one nm frame; then
//	               one byte carrying Chrome's stdin, stdout and stderr
//	               (SCM_RIGHTS)
//	agent → relay  one nm frame: "" once the session runs, else why it
//	               doesn't; then the end of the stream once it exits
//
// The relay stands where the host stood, so the session goes with it.
package agent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Kwangseok-Seo/paneterm/internal/nm"
	"github.com/Kwangseok-Seo/paneterm/internal/session"
)

// Needed says whether sessions must run under the agent, which only macOS
// asks for.
const Needed = runtime.GOOS == "darwin"

// Path is where the agent listens: ~/Library/Application Support/paneterm
// on macOS.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "paneterm", "agent.sock"), nil
}

// Listen listens on path, in place of any socket an earlier agent left
// there. Only the user may open its folder, so only the user's processes
// can connect, and those could start a shell anyway.
func Listen(path string) (*net.UnixListener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves a folder that is already there as it was.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
}

// Serve starts a command from command for each relay that connects to l,
// on the pipes and the environment the relay hands over, until l fails.
func Serve(l *net.UnixListener, command func() *exec.Cmd) error {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			if err := run(c, command()); err != nil {
				fmt.Fprintln(os.Stderr, "paneterm-host agent:", err)
			}
		}()
	}
}

// run starts cmd for the relay on c, tells the relay how that went, and
// waits for cmd.
func run(c *net.UnixConn, cmd *exec.Cmd) error {
	err := start(c, cmd)
	why := ""
	if err != nil {
		why = err.Error()
	}
	nm.Write(c, why)
	if err != nil {
		return err
	}
	// Chrome ends a tab by closing its pipes and killing the process it
	// started, which is now the relay. A session stuck writing to its
	// terminal never sees the pipes close, so it goes with the relay, as
	// it went with the host before.
	go func() {
		io.Copy(io.Discard, c)
		cmd.Process.Kill()
	}()
	return cmd.Wait()
}

// start reads what the relay hands over on c and starts cmd on it.
func start(c *net.UnixConn, cmd *exec.Cmd) error {
	// A relay sends it all at once, so one that stalls is dropped.
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	var env []byte
	if err := nm.Read(c, &env); err != nil {
		return fmt.Errorf("reading the environment: %w", err)
	}
	// Bytes, not JSON strings, which would replace any that aren't UTF-8.
	// Empty, not nil, which would hand the session the agent's own.
	cmd.Env = []string{}
	if len(env) > 0 {
		cmd.Env = strings.Split(string(env), "\x00")
	}
	files, err := receive(c)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = files[0], files[1], files[2]
	err = cmd.Start()
	// The command has its own copies. Chrome sees the end of the output
	// once every copy of its pipe is closed.
	for _, f := range files {
		f.Close()
	}
	return err
}

// receive reads the byte that carries the relay's three files.
func receive(c *net.UnixConn) ([]*os.File, error) {
	oob := make([]byte, syscall.CmsgSpace(3*4))
	// On macOS the files arrive open across exec, and net marks them
	// close-on-exec only after the read. Holding off forks until then
	// keeps a command started for another tab from taking a copy, which
	// would hold this tab's output open for as long as that tab runs.
	// (The connection itself is accepted without such a guard, as net
	// does on macOS; a copy of it only keeps a relay waiting until Chrome
	// kills it.) The relay sends the files right behind the environment,
	// so the deadline only bounds how long a stuck relay could hold off
	// every other tab.
	syscall.ForkLock.RLock()
	c.SetReadDeadline(time.Now().Add(time.Second))
	_, oobn, _, _, err := c.ReadMsgUnix(make([]byte, 1), oob)
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("reading the files: %w", err)
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, m := range msgs {
		if more, err := syscall.ParseUnixRights(&m); err == nil {
			fds = append(fds, more...)
		}
	}
	if len(fds) != 3 {
		for _, fd := range fds {
			syscall.Close(fd)
		}
		return nil, fmt.Errorf("relay sent %d files, want 3", len(fds))
	}
	return []*os.File{os.NewFile(uintptr(fds[0]), "stdin"), os.NewFile(uintptr(fds[1]), "stdout"), os.NewFile(uintptr(fds[2]), "stderr")}, nil
}

// Relay hands in, out and errOut, the pipes Chrome gave this process, and
// env to the agent at path, then waits until the session the agent starts
// on them exits. When there is no session, Relay says why on out, as a
// message the panel shows.
func Relay(path string, env []string, in, out, errOut *os.File) error {
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return tell(out, fmt.Errorf("can't reach the paneterm agent (%w); run paneterm-host install", err))
	}
	defer c.Close()
	if err := handOff(c, env, in, out, errOut); err != nil {
		return tell(out, fmt.Errorf("the paneterm agent didn't start a shell: %w", err))
	}
	// Chrome sees the end of the output only once this copy is closed too.
	in.Close()
	out.Close()
	_, err = io.Copy(io.Discard, c)
	return err
}

// handOff sends env and the files on c and returns the agent's answer.
func handOff(c *net.UnixConn, env []string, in, out, errOut *os.File) error {
	if err := nm.Write(c, []byte(strings.Join(env, "\x00"))); err != nil {
		return err
	}
	rights := syscall.UnixRights(int(in.Fd()), int(out.Fd()), int(errOut.Fd()))
	if _, _, err := c.WriteMsgUnix([]byte{0}, rights, nil); err != nil {
		return err
	}
	var why string
	if err := nm.Read(c, &why); err != nil {
		return err
	}
	if why != "" {
		return errors.New(why)
	}
	return nil
}

// tell reports err to the panel on out and returns it.
func tell(out *os.File, err error) error {
	nm.Write(out, session.Message{T: "error", Msg: err.Error()})
	return err
}
