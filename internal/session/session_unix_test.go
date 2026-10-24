//go:build unix

package session

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

// useTestShell makes startPanel run a plain sh with a fixed prompt, not
// the developer's shell and rc files.
func useTestShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")
}

func TestShellRunsCommands(t *testing.T) {
	p := startPanel(t)
	// The shell computes 6*7, so seeing 42 proves it ran, not just echoed.
	p.typeLine(`echo "sum=$((6*7))"`)
	p.waitOutput("sum=42")
}

func TestSignalExitReportsShellConvention(t *testing.T) {
	p := startPanel(t)
	p.typeLine("kill -9 $$")
	if code := p.waitExit(); code != 128+9 {
		t.Fatalf("exit code = %d, want 137 (128+SIGKILL)", code)
	}
}

// A job left holding the terminal must not keep the panel from learning
// that the shell is gone.
func TestExitWithBackgroundJobHoldingPTY(t *testing.T) {
	p := startPanel(t)
	p.typeLine(`sleep 30 & echo "job=$!:"`)
	job := p.waitNumber("job")
	t.Cleanup(func() { syscall.Kill(job, syscall.SIGKILL) })
	p.typeLine("exit")
	start := time.Now()
	p.waitExit()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("exit took %v with a background job alive", d)
	}
}

func TestResizeReachesShell(t *testing.T) {
	p := startPanel(t)
	p.typeLine("stty size")
	p.waitOutput("24 80")
	p.send(Message{T: "resize", Cols: 132, Rows: 50})
	p.typeLine("stty size")
	p.waitOutput("50 132")
}

func TestLocaleDefaultsToUTF8(t *testing.T) {
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	p := startPanel(t)
	p.typeLine(`echo "lang=$LANG"`)
	p.waitOutput("lang=" + fallbackLocale())
}

func TestNonASCIIRoundTrips(t *testing.T) {
	p := startPanel(t)
	p.typeLine("printf 'got:%s\\n' 한글")
	p.waitOutput("got:한글")
}

// Closing the port must not leave the shell behind.
func TestDisconnectHangsUpShell(t *testing.T) {
	p := startPanel(t)
	p.typeLine(`echo "pid=$$:"`)
	pid := p.waitNumber("pid")
	// Let the host go idle in its PTY read: there, closing the PTY alone
	// does not reach the shell.
	time.Sleep(200 * time.Millisecond)

	p.in.Close()
	if err := p.wait(); err != nil {
		t.Fatalf("Run = %v after disconnect", err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		// Signal 0 checks existence. The test reaps nothing, so a zombie
		// would still count as alive; Run's cmd.Wait is what reaps it.
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell %d still alive 3s after disconnect", pid)
		}
	}
}

// A newer panel may send types an older host doesn't know.
func TestUnknownMessageKeepsSession(t *testing.T) {
	p := startPanel(t)
	p.send(Message{T: "bogus"})
	m := p.waitFor("error message", func(m Message) bool { return m.T == "error" })
	if !strings.Contains(m.Msg, "bogus") {
		t.Fatalf("error = %q, want it to name the bad type", m.Msg)
	}
	p.typeLine(`echo "still=$((1+1))"`)
	p.waitOutput("still=2")
}
