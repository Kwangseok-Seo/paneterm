package nm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, map[string]string{"t": "ping"}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if n := binary.NativeEndian.Uint32(b[:4]); int(n) != len(b)-4 {
		t.Fatalf("length prefix %d, body %d bytes", n, len(b)-4)
	}
	if got := string(b[4:]); got != `{"t":"ping"}` {
		t.Fatalf("body %q", got)
	}
}

func TestRoundTripThenEOF(t *testing.T) {
	var buf bytes.Buffer
	for _, s := range []string{"a", "한글"} {
		if err := Write(&buf, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"a", "한글"} {
		var got string
		if err := Read(&buf, &got); err != nil || got != want {
			t.Fatalf("Read = %q, %v; want %q", got, err, want)
		}
	}
	var s string
	if err := Read(&buf, &s); !errors.Is(err, io.EOF) {
		t.Fatalf("Read on empty input = %v, want io.EOF", err)
	}
}

func TestReadTruncatedBody(t *testing.T) {
	frame := binary.NativeEndian.AppendUint32(nil, 10)
	frame = append(frame, `{"t"`...)
	var v any
	if err := Read(bytes.NewReader(frame), &v); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Read = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestWriteRefusesOversize(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, strings.Repeat("x", MaxOutgoing))
	if err == nil {
		t.Fatal("Write accepted a message over the limit")
	}
	if buf.Len() != 0 {
		t.Fatalf("Write sent %d bytes for a refused message", buf.Len())
	}
}
