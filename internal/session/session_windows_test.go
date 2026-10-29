//go:build windows

package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// useTestShell keeps the developer's profile out of the tests, and the
// tests' commands out of the developer's history. The shell itself is
// shellPath's choice: the commands below run the same in pwsh and in
// Windows PowerShell.
func useTestShell(t *testing.T) {
	shellFlags = []string{"-NoLogo", "-NoProfile", "-NoExit", "-Command", "Set-PSReadLineOption -HistorySaveStyle SaveNothing"}
	t.Cleanup(func() { shellFlags = nil })
}

func TestShellRunsCommands(t *testing.T) {
	p := startPanel(t)
	// The shell computes 6*7, so seeing 42 proves it ran, not just echoed.
	p.typeLine(`"sum=$(6*7)"`)
	p.waitOutput("sum=42")
}

// Chrome may hand down a PATH without PowerShell on it.
func TestShellStartsWithoutPath(t *testing.T) {
	t.Setenv("PATH", "")
	p := startPanel(t)
	p.typeLine(`"sum=$(6*7)"`)
	p.waitOutput("sum=42")
}

func TestNegativeExitCodeStaysNegative(t *testing.T) {
	p := startPanel(t)
	p.typeLine("exit -1")
	if code := p.waitExit(); code != -1 {
		t.Fatalf("exit code = %d, want -1", code)
	}
}

// The console writes once more as it closes, after the shell is gone: it
// turns off the modes it turned on, focus reporting among them. That has
// to reach the panel before the exit, after which the panel stops reading.
func TestClosingOutputArrivesBeforeExit(t *testing.T) {
	p := startPanel(t)
	p.typeLine("exit 4")
	p.waitExit()
	out := p.out.String()
	on := strings.LastIndex(out, "\x1b[?1004h")
	if on < 0 {
		t.Skip("this console doesn't turn on focus reporting")
	}
	if !strings.Contains(out[on:], "\x1b[?1004l") {
		t.Fatalf("focus reporting still on at the exit; output ends %q", out[max(0, len(out)-40):])
	}
}

// A process left attached to the console must not keep the panel from
// learning that the shell is gone, and closing the console ends it too.
func TestExitWithProcessLeftOnConsole(t *testing.T) {
	p := startPanel(t)
	p.typeLine(`$j = Start-Process ping -ArgumentList '-n','30','127.0.0.1' -NoNewWindow -PassThru; "job=$($j.Id):"`)
	job := p.waitNumber("job")
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(job))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	defer windows.TerminateProcess(h, 1)

	p.typeLine("exit")
	start := time.Now()
	p.waitExit()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("exit took %v with a process left on the console", d)
	}
	if ev, _ := windows.WaitForSingleObject(h, 3000); ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("process %d still alive 3s after the console closed", job)
	}
}

func TestResizeReachesShell(t *testing.T) {
	p := startPanel(t)
	size := `"size=$([Console]::WindowWidth)x$([Console]::WindowHeight):"`
	p.typeLine(size)
	p.waitOutput("size=80x24:")
	p.send(Message{T: "resize", Cols: 132, Rows: 50})
	p.typeLine(size)
	p.waitOutput("size=132x50:")
}

// A panel size a console can't take must not end the session.
func TestOutOfRangeSizeKeepsSession(t *testing.T) {
	p := startPanel(t)
	p.send(Message{T: "resize", Cols: 40000, Rows: 0})
	p.typeLine(`"size=$([Console]::WindowWidth)x$([Console]::WindowHeight):"`)
	p.waitOutput("size=32766x1:")
}

// Whatever terminal Chrome was started from, and however it spelled the
// name: Windows reads names in any case.
func TestTermProgramNamesPaneterm(t *testing.T) {
	// Setting a name that exists keeps its spelling, so unset it first.
	t.Setenv("TERM_PROGRAM", "")
	os.Unsetenv("TERM_PROGRAM")
	t.Setenv("Term_Program", "vscode")
	t.Setenv("TERM_PROGRAM_VERSION", "1.95.0")
	p := startPanel(t)
	p.typeLine(`"term=$env:TERM_PROGRAM ver=$($null -eq $env:TERM_PROGRAM_VERSION)"`)
	p.waitOutput("term=paneterm ver=True")
}

// The rest of the environment reaches the shell, non-ASCII intact.
func TestEnvironmentPassesThrough(t *testing.T) {
	t.Setenv("PANETERM_TEST", "한글")
	p := startPanel(t)
	p.typeLine(`"got:" + $env:PANETERM_TEST`)
	p.waitOutput("got:한글")
}

func TestNonASCIIRoundTrips(t *testing.T) {
	p := startPanel(t)
	// The shell joins the pieces, so the echoed command line can't match.
	p.typeLine(`"got:" + "한" + "글"`)
	p.waitOutput("got:한글")
}

// Even when the host was started ignoring Ctrl-C, as a parent can arrange
// and its children inherit.
func TestCtrlCInterruptsCommand(t *testing.T) {
	setConsoleCtrlHandler.Call(0, 1)
	t.Cleanup(func() { setConsoleCtrlHandler.Call(0, 0) })
	p := startPanel(t)
	// The marker shows the line is running, so Ctrl-C lands on Start-Sleep.
	p.typeLine(`"start=$(1+1)"; Start-Sleep 30`)
	p.waitOutput("start=2")
	p.send(Message{T: "in", D: []byte{0x03}})
	// Input typed before the prompt is back is dropped with the command.
	p.waitText("prompt after Ctrl-C", func(out string) bool {
		return strings.Contains(out[strings.Index(out, "start=2"):], "PS ")
	})
	p.typeLine(`"after=$(2+2)"`)
	p.waitOutput("after=4")
}

// Closing the port must not leave the shell behind.
func TestDisconnectHangsUpShell(t *testing.T) {
	p := startPanel(t)
	p.typeLine(`"pid=$($PID):"`)
	pid := p.waitNumber("pid")
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	// Let the host go idle in its output read, which has to be cancelled.
	time.Sleep(200 * time.Millisecond)

	p.in.Close()
	if err := p.wait(); err != nil {
		t.Fatalf("Run = %v after disconnect", err)
	}
	if ev, _ := windows.WaitForSingleObject(h, 3000); ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("shell %d still alive 3s after disconnect", pid)
	}
}

// Chrome may close the port while the shell is printing and nothing reads
// the output any more. The console must still close.
func TestDisconnectWhileOutputUnread(t *testing.T) {
	p := startPanel(t)
	p.typeLine(`"pid=$($PID):"; while ($true) { "x" * 200 }`)
	pid := p.waitNumber("pid")
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	// Stop reading: the panel's buffer and then every pipe behind it fill.
	time.Sleep(time.Second)

	p.in.Close()
	if err := p.wait(); err != nil {
		t.Fatalf("Run = %v after disconnect", err)
	}
	if ev, _ := windows.WaitForSingleObject(h, 3000); ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("shell %d still alive 3s after disconnect", pid)
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
	p.typeLine(`"still=$(1+1)"`)
	p.waitOutput("still=2")
}
