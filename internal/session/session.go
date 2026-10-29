// Package session runs one terminal session for one tab of the side panel:
// Chrome starts a host process per port, and the host serves exactly one
// shell.
package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Kwangseok-Seo/paneterm/internal/nm"
)

// Message is the one shape used in both directions; T says which fields
// apply.
//
//	panel → host  open {cols, rows} · in {d} · resize {cols, rows}
//	host → panel  out {d} · exit {code} · error {msg}
//
// D is raw bytes, which encoding/json carries as base64: PTY output can
// split a UTF-8 sequence across reads, and a JSON string can't hold half
// a character.
type Message struct {
	T    string `json:"t"`
	D    []byte `json:"d,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Code *int   `json:"code,omitempty"`
	Msg  string `json:"msg,omitempty"`
}

// chunk caps the PTY bytes per "out" message. Base64 grows it by a third,
// which stays far under nm.MaxOutgoing.
const chunk = 32 * 1024

// drain is how long an exited shell's last output gets to reach the panel.
// The output normally ends at once: a Unix PTY closes with the shell, and a
// Windows console as soon as it is closed. A background job still holding a
// PTY, or a console slow to close, would otherwise hold back the exit
// forever.
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

// readOpen reads the panel's first message, which must be "open". A nil
// result means Run should return err: nil if Chrome closed the port first,
// otherwise a failure already reported to the panel.
func readOpen(in io.Reader, s *sender) (*Message, error) {
	var open Message
	if err := nm.Read(in, &open); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, s.fail(err)
	}
	if open.T != "open" {
		return nil, s.fail(fmt.Errorf(`first message must be "open", got %q`, open.T))
	}
	return &open, nil
}

// pumpOutput forwards PTY output until it ends: on Unix once the shell and
// everything it left holding the terminal are gone, on Windows once the
// pseudo console is closed.
func pumpOutput(pty io.Reader, s *sender) {
	buf := make([]byte, chunk)
	for {
		n, err := pty.Read(buf)
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
func pumpInput(in io.Reader, pty io.Writer, resize func(cols, rows uint16) error, s *sender) error {
	for {
		var m Message
		if err := nm.Read(in, &m); err != nil {
			return err
		}
		switch m.T {
		case "in":
			if _, err := pty.Write(m.D); err != nil {
				return err
			}
		case "resize":
			if err := resize(m.Cols, m.Rows); err != nil {
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

// shellEnv is the environment the shell starts with: this process's, with
// TERM_PROGRAM=paneterm and set in place of whatever it had under those
// names. TERM_PROGRAM is where terminals put their name (Apple_Terminal,
// iTerm.app, vscode), so rc files can set up paneterm's shells alone. One
// handed down from whatever started Chrome names a terminal the shell is
// not in, and so does the TERM_PROGRAM_VERSION beside it, which goes too.
func shellEnv(set ...string) []string {
	set = append([]string{"TERM_PROGRAM=paneterm"}, set...)
	drop := []string{"TERM_PROGRAM_VERSION"}
	for _, kv := range set {
		name, _, _ := strings.Cut(kv, "=")
		drop = append(drop, name)
	}
	same := func(a, b string) bool { return a == b }
	if runtime.GOOS == "windows" {
		same = strings.EqualFold // Windows matches names in any case
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(drop, func(d string) bool { return same(name, d) })
	})
	return append(env, set...)
}
