// Package session runs one terminal session for one side panel: Chrome
// starts a host process per port, and the host serves exactly one shell.
package session

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
