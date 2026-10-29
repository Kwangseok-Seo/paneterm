//go:build unix

package agent

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Kwangseok-Seo/paneterm/internal/nm"
	"github.com/Kwangseok-Seo/paneterm/internal/session"
)

// TestMain doubles as the command the agent starts, the way it starts
// paneterm-host serve: a relay's environment can tell the test binary to
// serve a session instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv("PANETERM_TEST_SERVE") == "1" {
		if err := session.Run(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sockPath is a socket path in a fresh folder. t.TempDir's names can pass
// the 104 bytes macOS allows a socket path.
func sockPath(t *testing.T) string {
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

// chrome stands in for Chrome: it holds its ends of the pipes it gives
// the host, which relays them.
type chrome struct {
	t     *testing.T
	in    *os.File // the host's input
	msgs  chan session.Message
	ended chan struct{} // closed once the host's output ends
	done  chan error    // Relay's result
	out   strings.Builder
}

func startRelay(t *testing.T, sock string, env []string) *chrome {
	t.Helper()
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	c := &chrome{t: t, in: inW, msgs: make(chan session.Message, 64), ended: make(chan struct{}), done: make(chan error, 1)}
	go func() { c.done <- Relay(sock, env, inR, outW, errW) }()
	go func() {
		defer close(c.ended)
		for {
			var m session.Message
			if nm.Read(outR, &m) != nil {
				return
			}
			c.msgs <- m
		}
	}()
	t.Cleanup(func() {
		for _, f := range []*os.File{inR, inW, outR, outW, errR, errW} {
			f.Close()
		}
	})
	return c
}

func (c *chrome) send(m session.Message) {
	c.t.Helper()
	if err := nm.Write(c.in, m); err != nil {
		c.t.Fatal(err)
	}
}

// waitFor reads messages until one satisfies ok, failing after 5s.
func (c *chrome) waitFor(what string, ok func(session.Message) bool) session.Message {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-c.msgs:
			if m.T == "out" {
				c.out.Write(m.D)
			}
			if ok(m) {
				return m
			}
		case <-c.ended:
			c.t.Fatalf("output ended before %s; output so far:\n%s", what, c.out.String())
		case <-deadline:
			c.t.Fatalf("timed out waiting for %s; output so far:\n%s", what, c.out.String())
		}
	}
}

func (c *chrome) waitOutput(sub string) {
	c.t.Helper()
	if !strings.Contains(c.out.String(), sub) {
		c.waitFor(sub, func(session.Message) bool { return strings.Contains(c.out.String(), sub) })
	}
}

// startAgent serves relays on a fresh socket, starting command for each.
func startAgent(t *testing.T, command func() *exec.Cmd) string {
	t.Helper()
	sock := sockPath(t)
	l, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go Serve(l, command)
	return sock
}

// The relay's pipes and environment reach a session the agent starts, and
// Chrome learns that it ended the way it would from a host of its own.
func TestRelayRunsSession(t *testing.T) {
	// Should the relay's environment not reach it, the test binary runs no
	// tests rather than this one again.
	sock := startAgent(t, func() *exec.Cmd { return exec.Command(os.Args[0], "-test.run=^$") })
	// A connection that hands nothing over must not stop the agent.
	if junk, err := net.Dial("unix", sock); err == nil {
		junk.Close()
	}

	// The agent's own environment, which the session must not see.
	t.Setenv("PANETERM_AGENT_ONLY", "leaked")
	env := []string{"PANETERM_TEST_SERVE=1", "PANETERM_RELAYED=yes", "PANETERM_BYTES=\xff", "SHELL=/bin/sh", "PS1=$ "}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PANETERM_AGENT_ONLY=") {
			env = append(env, kv)
		}
	}
	c := startRelay(t, sock, env)
	c.send(session.Message{T: "open", Cols: 80, Rows: 24})
	c.send(session.Message{T: "in", D: []byte(`echo "sum=$((6*7)) env=$PANETERM_RELAYED agent=${PANETERM_AGENT_ONLY-unset}"` + "\r")})
	c.waitOutput("sum=42 env=yes agent=unset")
	// A byte that isn't UTF-8 arrives as it was.
	c.send(session.Message{T: "in", D: []byte(`echo "bytes=$(printf %s "$PANETERM_BYTES" | od -An -tx1 | tr -d ' ')"` + "\r")})
	c.waitOutput("bytes=ff")

	c.send(session.Message{T: "in", D: []byte("exit 3\r")})
	m := c.waitFor("exit message", func(m session.Message) bool { return m.T == "exit" })
	if m.Code == nil || *m.Code != 3 {
		t.Fatalf("exit message %+v, want code 3", m)
	}
	// Chrome takes the end of the output as the host's exit, so no copy of
	// the pipe may outlive the session: not the relay's, not the agent's.
	select {
	case <-c.ended:
	case <-time.After(3 * time.Second):
		t.Fatal("output still open 3s after the session exited")
	}
	select {
	case err := <-c.done:
		if err != nil {
			t.Fatalf("Relay = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Relay still waiting 3s after the session exited")
	}
}

// Chrome ends a tab by killing the process it started, now the relay. The
// session must end with it, even one that never sees its input close.
func TestSessionEndsWithRelay(t *testing.T) {
	sock := startAgent(t, func() *exec.Cmd { return exec.Command("/bin/sh", "-c", `echo "pid=$$:"; exec sleep 30`) })
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer func() {
		for _, f := range []*os.File{inR, inW, outR, outW} {
			f.Close()
		}
	}()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handOff(conn, os.Environ(), inR, outW, os.Stderr); err != nil {
		t.Fatal(err)
	}
	outW.Close()
	line := make([]byte, 64)
	n, _ := outR.Read(line)
	var pid int
	if _, err := fmt.Sscanf(string(line[:n]), "pid=%d:", &pid); err != nil {
		t.Fatalf("no pid in %q", line[:n])
	}

	conn.Close() // the relay is gone; inW, Chrome's end, stays open
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %d still alive 3s after its relay went", pid)
		}
	}
}

// The panel learns why when the agent can't start the session.
func TestFailedStartTellsPanel(t *testing.T) {
	sock := startAgent(t, func() *exec.Cmd { return exec.Command("/nonexistent/paneterm-host", "serve") })
	c := startRelay(t, sock, os.Environ())
	m := c.waitFor("error message", func(m session.Message) bool { return m.T == "error" })
	if !strings.Contains(m.Msg, "/nonexistent/paneterm-host") {
		t.Fatalf("error = %q, want it to say what failed", m.Msg)
	}
	if err := <-c.done; err == nil {
		t.Fatal("Relay = nil with no session")
	}
}

func TestRelayWithoutAgentTellsPanel(t *testing.T) {
	c := startRelay(t, sockPath(t), os.Environ())
	m := c.waitFor("error message", func(m session.Message) bool { return m.T == "error" })
	if !strings.Contains(m.Msg, "paneterm-host install") {
		t.Fatalf("error = %q, want it to say how to start the agent", m.Msg)
	}
	if err := <-c.done; err == nil {
		t.Fatal("Relay = nil with no agent")
	}
}

// Only the user may reach the socket, whatever an earlier agent left.
func TestListenMakesFolderPrivate(t *testing.T) {
	sock := sockPath(t)
	dir := filepath.Dir(sock)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket folder mode = %v, want 0700", fi.Mode().Perm())
	}
}
