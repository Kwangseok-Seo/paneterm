//go:build windows

package session

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Run waits for "open", starts PowerShell on a pseudo console, and pumps
// bytes both ways until the shell exits or Chrome closes the port.
func Run(in io.Reader, out io.Writer) error {
	s := &sender{w: out}
	open, err := readOpen(in, s)
	if open == nil {
		return err
	}

	con, err := startShell(open.Cols, open.Rows)
	if err != nil {
		return s.fail(err)
	}
	// Both ways out start closing the console aside, because closing is
	// documented to wait until its output is read or the pipe is broken.
	// Closing the output then breaks the pipe if nothing is reading it, or
	// waits out the read in progress, which ends once the console is gone.
	defer con.out.Close()

	outputDone := make(chan struct{})
	go func() {
		pumpOutput(con.out, s)
		close(outputDone)
	}()

	var code int
	exited := make(chan error, 1)
	go func() {
		var err error
		code, err = con.wait()
		exited <- err
	}()

	disconnected := make(chan error, 1)
	go func() { disconnected <- pumpInput(in, con.in, con.resize, s) }()

	select {
	case err := <-exited:
		// Unlike a Unix PTY, the console outlives the shell, and writes once
		// more as it closes: its output ends only then.
		go con.close()
		select {
		case <-outputDone:
		case <-time.After(drain):
		}
		if err != nil {
			return s.fail(err)
		}
		return s.send(Message{T: "exit", Code: &code})
	case err := <-disconnected:
		go con.close() // which ends the shell
		if errors.Is(err, io.EOF) {
			return nil
		}
		return s.fail(err)
	}
}

// console is a shell on a pseudo console (ConPTY), Windows' counterpart of
// a PTY.
type console struct {
	hpc  windows.Handle
	in   *os.File // keystrokes, as the panel sends them
	out  *os.File // the screen, as VT sequences
	proc windows.Handle

	// mu keeps resize off a closed console, whose handle points to freed
	// memory.
	mu     sync.Mutex
	closed bool
}

func startShell(cols, rows uint16) (*console, error) {
	if createPseudoConsole.Find() != nil {
		return nil, errors.New("paneterm needs Windows 10 version 1809 or later")
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	// The console keeps its own copies of its ends. Ours must go, or the
	// output would never end.
	defer windows.CloseHandle(inR)
	defer windows.CloseHandle(outW)
	c := &console{in: os.NewFile(uintptr(inW), "console input"), out: os.NewFile(uintptr(outR), "console output")}

	if err := windows.CreatePseudoConsole(coord(cols, rows), inR, outW, 0, &c.hpc); err != nil {
		c.in.Close()
		c.out.Close()
		return nil, fmt.Errorf("pseudo console: %w", err)
	}
	var err error
	if c.proc, err = spawn(c.hpc, shellPath()); err != nil {
		c.out.Close()
		c.close()
		return nil, err
	}
	return c, nil
}

// coord turns a panel size into a console size: 1 to 32766 cells each way.
// Sizes take int16, but at 32767 the console takes the size and then ends
// the shell (Windows 11 build 26300).
func coord(cols, rows uint16) windows.Coord {
	fit := func(n uint16) int16 { return int16(min(max(n, 1), math.MaxInt16-1)) }
	return windows.Coord{X: fit(cols), Y: fit(rows)}
}

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	// SetConsoleCtrlHandler(NULL, ignore) sets whether this process, and
	// the processes it starts, ignore Ctrl-C. x/sys doesn't wrap it.
	setConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	// Only looked for: x/sys's wrapper panics where Windows lacks it, which
	// is before Windows 10 version 1809.
	createPseudoConsole = kernel32.NewProc("CreatePseudoConsole")
)

// shellFlags go after the shell's path. Tests use them to keep the
// developer's profile and history out (see useTestShell).
var shellFlags []string

// shellPath prefers PowerShell 7 and falls back to the Windows PowerShell
// that every Windows ships. Chrome hands down the PATH it started with,
// which may predate a PowerShell 7 install or lack either, so both are also
// looked for in the folders they install to.
func shellPath() string {
	if path, err := exec.LookPath("pwsh.exe"); err == nil {
		return path
	}
	if pf, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0); err == nil {
		if path, err := exec.LookPath(filepath.Join(pf, "PowerShell", "7", "pwsh.exe")); err == nil {
			return path
		}
	}
	sys, _ := windows.GetSystemDirectory()
	return filepath.Join(sys, "WindowsPowerShell", "v1.0", "powershell.exe")
}

// spawn starts shell attached to the console hpc, in the user's home
// folder. os/exec can't attach a process to a pseudo console.
func spawn(hpc windows.Handle, shell string) (windows.Handle, error) {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, err
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not its address.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return 0, err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// Without this the shell takes this process's standard handles, which
	// are Chrome's pipes, instead of the console's.
	si.Flags = windows.STARTF_USESTDHANDLES

	app, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		return 0, err
	}
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{shell}, shellFlags...)))
	if err != nil {
		return 0, err
	}
	var dir *uint16
	if home, err := os.UserHomeDir(); err == nil {
		dir, _ = windows.UTF16PtrFromString(home)
	}
	// Whoever started this process may have had it ignore Ctrl-C, which the
	// shell would inherit. A terminal's shell must not.
	setConsoleCtrlHandler.Call(0, 0)
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(app, cmdline, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, dir, &si.StartupInfo, &pi); err != nil {
		return 0, fmt.Errorf("start %s: %w", shell, err)
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}

// wait returns the shell's exit code once it exits, and closes the process
// handle, which nothing else uses. Windows exit codes are 32 bits, read here
// as signed the way %ERRORLEVEL% and $LASTEXITCODE read them, so "exit -1"
// comes back as -1; any code can be real, so a failure is an error.
func (c *console) wait() (int, error) {
	defer windows.CloseHandle(c.proc)
	if _, err := windows.WaitForSingleObject(c.proc, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("wait for the shell: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.proc, &code); err != nil {
		return 0, fmt.Errorf("shell's exit code: %w", err)
	}
	return int(int32(code)), nil
}

func (c *console) resize(cols, rows uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil // the shell is gone; nothing is left to fit
	}
	return windows.ResizePseudoConsole(c.hpc, coord(cols, rows))
}

// close ends the console and every process still attached to it. The output
// ends once the console has written its last frame.
func (c *console) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	windows.ClosePseudoConsole(c.hpc)
	c.in.Close()
}
