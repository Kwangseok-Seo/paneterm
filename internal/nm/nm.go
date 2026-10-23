// Package nm implements Chrome's native messaging framing: each message is
// UTF-8 JSON preceded by its length as a 32-bit integer in native byte order.
package nm

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MaxOutgoing is Chrome's limit on a single message from the host.
const MaxOutgoing = 1024 * 1024

// Read decodes one message from r into v. It returns io.EOF when r closes
// between messages, which is how Chrome says the port is gone.
func Read(r io.Reader, v any) error {
	var n uint32
	if err := binary.Read(r, binary.NativeEndian, &n); err != nil {
		return err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}

// Write encodes v as one message on w.
func Write(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > MaxOutgoing {
		return fmt.Errorf("nm: message is %d bytes, over Chrome's %d-byte limit", len(body), MaxOutgoing)
	}
	frame := binary.NativeEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body)))
	_, err = w.Write(append(frame, body...))
	return err
}
