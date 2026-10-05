// Package ipc defines the wire protocol between the kitmux daemon and the
// kitmux subcommands kitty runs (the pane proxy and the key actions).
//
// Every connection starts with one JSON request line. Key actions get one JSON
// response line back and the connection closes. A pane proxy's connection
// switches to binary frames after its request line.
package ipc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Op names what a connection is for.
type Op string

const (
	// OpPane registers a pane proxy; frames follow the request line.
	OpPane Op = "pane"
	// OpSplit splits the pane shown in a kitty window.
	OpSplit Op = "split"
	// OpNewTab opens a new tmux window on the target of a kitty window.
	OpNewTab Op = "newtab"
	// OpDescribe asks for the tab title of a kitty window, for the
	// confirmation prompt.
	OpDescribe Op = "describe"
	// OpKill kills the tmux pane or window shown in a kitty window.
	OpKill Op = "kill"
)

// Scope says whether a kill or describe request is about a pane or about its
// whole tmux window.
type Scope string

const (
	// ScopePane is the single pane shown in the kitty window.
	ScopePane Scope = "pane"
	// ScopeWindow is the tmux window (the kitty tab) the pane belongs to.
	ScopeWindow Scope = "window"
)

// Code is a machine-readable result code of a key action.
type Code string

const (
	// CodeNotManaged means the kitty window does not show a tmux pane.
	CodeNotManaged Code = "NOT_MANAGED"
	// CodeDisconnected means the pane's target has no live tmux connection.
	CodeDisconnected Code = "DISCONNECTED"
	// CodeBadRequest means the request was malformed.
	CodeBadRequest Code = "BAD_REQUEST"
)

// Request is the first line of every connection.
type Request struct {
	Op Op `json:"op"`

	// Target, Pane, Cols and Rows belong to OpPane: the target's position
	// on the kitmux command line, the tmux pane id ("%3") and the size of
	// the proxy's terminal.
	Target int    `json:"target,omitempty"`
	Pane   string `json:"pane,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`

	// Window is the id of the kitty window a key action was triggered in.
	Window int `json:"window,omitempty"`
	// Stacked asks OpSplit for a split below instead of beside.
	Stacked bool `json:"stacked,omitempty"`
	// Scope belongs to OpDescribe and OpKill.
	Scope Scope `json:"scope,omitempty"`
}

// Response answers a key action.
type Response struct {
	OK   bool `json:"ok"`
	Code Code `json:"code,omitempty"`
	// Title is the kitty tab title, for OpDescribe.
	Title string `json:"title,omitempty"`
}

// FrameType tags a binary frame on a pane proxy connection.
type FrameType byte

const (
	// FrameOutput carries bytes for the proxy's terminal (daemon to proxy).
	FrameOutput FrameType = 'o'
	// FrameInput carries bytes read from the terminal (proxy to daemon).
	FrameInput FrameType = 'i'
	// FrameResize carries the terminal's new size (proxy to daemon).
	FrameResize FrameType = 'r'
	// FrameExit tells the proxy its pane is gone and it should exit (daemon
	// to proxy).
	FrameExit FrameType = 'x'
)

const (
	// MaxFramePayload bounds one frame, so a corrupt length cannot make the
	// reader allocate without limit.
	MaxFramePayload = 4 << 20

	frameHeaderSize = 5 // type byte + 32-bit big-endian payload length
	sizePayloadSize = 4 // two 16-bit big-endian numbers: cols, rows
	maxDimension    = 0xffff
)

// ErrFrameTooLarge is returned when a frame announces a payload above
// MaxFramePayload.
var ErrFrameTooLarge = errors.New("ipc frame too large")

// WriteJSON writes v as one JSON line.
func WriteJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode ipc message: %w", err)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// ReadJSON reads one JSON line into v.
func ReadJSON(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	err = json.Unmarshal(line, v)
	if err != nil {
		return fmt.Errorf("decode ipc message: %w", err)
	}
	return nil
}

// WriteFrame writes one frame. The header and payload go out in a single
// Write, so frames from concurrent writers that share a lock never interleave.
func WriteFrame(w io.Writer, kind FrameType, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return ErrFrameTooLarge
	}
	buf := make([]byte, frameHeaderSize+len(payload))
	buf[0] = byte(kind)
	binary.BigEndian.PutUint32(buf[1:frameHeaderSize], uint32(len(payload)))
	copy(buf[frameHeaderSize:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame. The returned payload is freshly allocated.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var header [frameHeaderSize]byte
	_, err := io.ReadFull(r, header[:])
	if err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > MaxFramePayload {
		return 0, nil, ErrFrameTooLarge
	}
	payload := make([]byte, size)
	_, err = io.ReadFull(r, payload)
	if err != nil {
		return 0, nil, err
	}
	return FrameType(header[0]), payload, nil
}

// EncodeSize builds the payload of a FrameResize.
func EncodeSize(cols, rows int) []byte {
	buf := make([]byte, sizePayloadSize)
	binary.BigEndian.PutUint16(buf[0:2], uint16(min(max(cols, 0), maxDimension)))
	binary.BigEndian.PutUint16(buf[2:4], uint16(min(max(rows, 0), maxDimension)))
	return buf
}

// DecodeSize reads the payload of a FrameResize.
func DecodeSize(payload []byte) (int, int, bool) {
	if len(payload) != sizePayloadSize {
		return 0, 0, false
	}
	cols := int(binary.BigEndian.Uint16(payload[0:2]))
	rows := int(binary.BigEndian.Uint16(payload[2:4]))
	return cols, rows, true
}
