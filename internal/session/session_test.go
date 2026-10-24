//go:build unix || windows

package session

import (
	"encoding/binary"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Kwangseok-Seo/paneterm/internal/nm"
)

// panel stands in for Chrome: it talks to Run over pipes and collects what
// the host sends back.
type panel struct {
	t    *testing.T
	in   *io.PipeWriter
	msgs chan Message
	done chan struct{} // closed when Run returns
	err  error         // Run's result, once done is closed
	out  strings.Builder
}

func startPanel(t *testing.T) *panel {
	t.Helper()
	useTestShell(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &panel{t: t, in: inW, msgs: make(chan Message, 64), done: make(chan struct{})}
	go func() {
		p.err = Run(inR, outW)
		close(p.done)
		outW.Close()
	}()
	go func() {
		defer close(p.msgs)
		for {
			var m Message
			if err := nm.Read(outR, &m); err != nil {
				return
			}
			p.msgs <- m
		}
	}()
	t.Cleanup(func() {
		inW.Close()
		go func() {
			for range p.msgs {
			}
		}()
		if err := p.wait(); err != nil {
			t.Errorf("Run = %v at the end of the test", err)
		}
	})
	p.send(Message{T: "open", Cols: 80, Rows: 24})
	return p
}

// wait returns Run's result, failing if Run has not returned within 3s.
func (p *panel) wait() error {
	p.t.Helper()
	select {
	case <-p.done:
		return p.err
	case <-time.After(3 * time.Second):
		p.t.Fatal("Run did not return within 3s")
		return nil
	}
}

// send fails the test if the host has stopped reading its input, rather
// than blocking on the pipe until the test binary times out.
func (p *panel) send(m Message) {
	p.t.Helper()
	sent := make(chan error, 1)
	go func() { sent <- nm.Write(p.in, m) }()
	select {
	case err := <-sent:
		if err != nil {
			p.t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		p.t.Fatalf("host stopped reading input; output so far:\n%s", p.out.String())
	}
}

func (p *panel) typeLine(s string) { p.send(Message{T: "in", D: []byte(s + "\r")}) }

// waitFor reads messages until one satisfies ok, failing after 5s.
func (p *panel) waitFor(what string, ok func(Message) bool) Message {
	p.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, open := <-p.msgs:
			if !open {
				p.t.Fatalf("host closed before %s; output so far:\n%s", what, p.out.String())
			}
			if m.T == "out" {
				p.out.Write(m.D)
			}
			if ok(m) {
				return m
			}
		case <-deadline:
			p.t.Fatalf("timed out waiting for %s; output so far:\n%s", what, p.out.String())
		}
	}
}

// waitText waits until the output so far satisfies ok, which it may
// already do: what it needs may have come in with earlier messages.
func (p *panel) waitText(what string, ok func(out string) bool) {
	p.t.Helper()
	if !ok(p.out.String()) {
		p.waitFor(what, func(Message) bool { return ok(p.out.String()) })
	}
}

func (p *panel) waitOutput(sub string) {
	p.t.Helper()
	p.waitText("output "+strconv.Quote(sub), func(out string) bool { return strings.Contains(out, sub) })
}

// waitNumber waits for "label=<digits>:" in the output and returns the
// digits. The echoed command line holds the shell expression instead.
func (p *panel) waitNumber(label string) int {
	p.t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(label) + `=(\d+):`)
	p.waitText(label, re.MatchString)
	n, _ := strconv.Atoi(re.FindStringSubmatch(p.out.String())[1])
	return n
}

func (p *panel) waitExit() int {
	p.t.Helper()
	m := p.waitFor("exit message", func(m Message) bool { return m.T == "exit" })
	if m.Code == nil {
		p.t.Fatal("exit message without a code")
	}
	return *m.Code
}

// "exit 3" reads the same in sh and in PowerShell.
func TestExitReportsCode(t *testing.T) {
	p := startPanel(t)
	p.typeLine("exit 3")
	if code := p.waitExit(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

// runRaw starts Run, writes frame to it, and returns the first message Run
// sends and Run's result.
func runRaw(t *testing.T, frame []byte) (Message, error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run(inR, outW); outW.Close() }()
	go func() { inW.Write(frame); inW.Close() }()
	var m Message
	if err := nm.Read(outR, &m); err != nil {
		t.Fatalf("no message from the host: %v", err)
	}
	go io.Copy(io.Discard, outR)
	return m, <-done
}

func TestFirstMessageMustBeOpen(t *testing.T) {
	var frame strings.Builder
	nm.Write(&frame, Message{T: "in", D: []byte("x")})
	m, err := runRaw(t, []byte(frame.String()))
	if m.T != "error" || err == nil {
		t.Fatalf("got %+v, Run = %v; want an error message and a failed Run", m, err)
	}
}

func TestUndecodableFirstFrameIsReported(t *testing.T) {
	body := []byte("{not json")
	frame := append(binary.NativeEndian.AppendUint32(nil, uint32(len(body))), body...)
	m, err := runRaw(t, frame)
	if m.T != "error" || err == nil {
		t.Fatalf("got %+v, Run = %v; want an error message and a failed Run", m, err)
	}
}
