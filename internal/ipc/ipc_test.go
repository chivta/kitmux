package ipc

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		kind    FrameType
		payload []byte
	}{
		{"output", FrameOutput, []byte("\x1b[1mhello\x1b[0m\r\n")},
		{"input", FrameInput, []byte{0x03}},
		{"empty exit", FrameExit, nil},
		{"binary", FrameOutput, []byte{0, 1, 2, 0xff, 0xfe}},
	}
	var buf bytes.Buffer
	for _, tc := range cases {
		err := WriteFrame(&buf, tc.kind, tc.payload)
		if err != nil {
			t.Fatalf("%s: WriteFrame: %v", tc.name, err)
		}
	}
	for _, tc := range cases {
		kind, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("%s: ReadFrame: %v", tc.name, err)
		}
		if kind != tc.kind || !bytes.Equal(payload, tc.payload) {
			t.Fatalf("%s: got %q %q, want %q %q", tc.name, kind, payload, tc.kind, tc.payload)
		}
	}
	_, _, err := ReadFrame(&buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read past the end: err = %v, want EOF", err)
	}
}

func TestFrameLimits(t *testing.T) {
	err := WriteFrame(io.Discard, FrameOutput, make([]byte, MaxFramePayload+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized write: err = %v", err)
	}
	header := []byte{byte(FrameOutput), 0xff, 0xff, 0xff, 0xff}
	_, _, err = ReadFrame(bytes.NewReader(header))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized read: err = %v", err)
	}
	_, _, err = ReadFrame(bytes.NewReader([]byte{byte(FrameOutput), 0, 0, 0, 5, 'a'}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated payload: err = %v", err)
	}
}

func TestSizeRoundTrip(t *testing.T) {
	cols, rows, ok := DecodeSize(EncodeSize(189, 61))
	if !ok || cols != 189 || rows != 61 {
		t.Fatalf("got %d %d %v", cols, rows, ok)
	}
	cols, rows, ok = DecodeSize(EncodeSize(-4, 1<<20))
	if !ok || cols != 0 || rows != maxDimension {
		t.Fatalf("clamping: got %d %d %v", cols, rows, ok)
	}
	_, _, ok = DecodeSize([]byte{1, 2, 3})
	if ok {
		t.Fatal("short payload must not decode")
	}
}

func TestJSONThenFrames(t *testing.T) {
	// A pane proxy connection: one JSON line, then frames on the same stream.
	var buf bytes.Buffer
	req := Request{Op: OpPane, Target: 1, Pane: "%12", Cols: 80, Rows: 24}
	err := WriteJSON(&buf, req)
	if err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	err = WriteFrame(&buf, FrameInput, []byte("ls\r"))
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	reader := bufio.NewReader(&buf)
	var got Request
	err = ReadJSON(reader, &got)
	if err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if got != req {
		t.Fatalf("request = %+v, want %+v", got, req)
	}
	kind, payload, err := ReadFrame(reader)
	if err != nil || kind != FrameInput || string(payload) != "ls\r" {
		t.Fatalf("frame = %q %q %v", kind, payload, err)
	}
}

func TestReadJSONRejectsGarbage(t *testing.T) {
	var req Request
	err := ReadJSON(bufio.NewReader(bytes.NewBufferString("not json\n")), &req)
	if err == nil {
		t.Fatal("expected an error")
	}
}
