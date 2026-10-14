package lanewire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestMessageRoundTripsHeaderAndRawBody(t *testing.T) {
	request := Request{
		Version:           ProtocolVersion,
		Harness:           "opencode",
		Event:             "session.http.request",
		HostVersion:       "2.0.20",
		HostVersionSource: "caller-supplied",
		ActorClaim:        "actor-1",
	}
	// The body is not valid UTF-8 on purpose: the frame must carry the host's
	// bytes verbatim, because the receipt hashes exactly these bytes.
	body := []byte{0xff, 0x00, 'a', '{', '"', 0xfe}

	var frame bytes.Buffer
	if err := WriteMessage(&frame, request, body); err != nil {
		t.Fatal(err)
	}
	var decoded Request
	gotBody, err := ReadMessage(&frame, &decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body = %v, want %v", gotBody, body)
	}
	if decoded != request {
		t.Fatalf("decoded = %+v, want %+v", decoded, request)
	}
	if frame.Len() != 0 {
		t.Fatalf("ReadMessage left %d bytes in the stream", frame.Len())
	}
}

func TestMessageRoundTripsACommittedResponse(t *testing.T) {
	response := Response{
		Version:      ProtocolVersion,
		Kind:         KindCommitted,
		ExitStatus:   1,
		Stdout:       []byte(`{"decision":"proceed"}`),
		OccurrenceID: "occ-1",
		ClaimState:   "written",
	}
	var frame bytes.Buffer
	if err := WriteMessage(&frame, response, nil); err != nil {
		t.Fatal(err)
	}
	var decoded Response
	body, err := ReadMessage(&frame, &decoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("a header-only message returned a %d-byte body", len(body))
	}
	if decoded.OccurrenceID != response.OccurrenceID || !bytes.Equal(decoded.Stdout, response.Stdout) || decoded.Kind != KindCommitted {
		t.Fatalf("decoded = %+v, want %+v", decoded, response)
	}
}

func TestReadMessageRefusesUnknownAndTrailingMembers(t *testing.T) {
	unknown := []byte(`{"version":1,"kind":"committed","surprise":true}`)
	if _, err := ReadMessage(frameWithHeader(unknown), &Response{}); err == nil {
		t.Fatal("an unknown header member was accepted")
	}
	trailing := []byte(`{"version":1,"kind":"committed"} {"version":1}`)
	if _, err := ReadMessage(frameWithHeader(trailing), &Response{}); err == nil {
		t.Fatal("a header followed by a second JSON object was accepted")
	}
}

func TestReadMessageRefusesOversizeFrames(t *testing.T) {
	oversizeHeader := make([]byte, 4)
	binary.BigEndian.PutUint32(oversizeHeader, uint32(MaxHeaderBytes+1))
	if _, err := ReadMessage(bytes.NewReader(oversizeHeader), &Request{}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("an oversize header returned %v, want ErrFrameTooLarge", err)
	}
	oversizeBody := make([]byte, 4)
	binary.BigEndian.PutUint32(oversizeBody, uint32(MaxBodyBytes+1))
	if _, err := ReadMessage(io.MultiReader(bytes.NewReader(frameHeaderPrefix(`{"version":1}`)), bytes.NewReader(oversizeBody)), &Request{}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("an oversize body returned %v, want ErrFrameTooLarge", err)
	}
}

func TestWriteMessageRefusesAnOversizeBody(t *testing.T) {
	if err := WriteMessage(io.Discard, Request{Version: ProtocolVersion}, make([]byte, MaxBodyBytes+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteMessage accepted an oversize body with %v", err)
	}
}

func TestCheckVersionRefusesADifferentProtocol(t *testing.T) {
	if err := CheckVersion(ProtocolVersion); err != nil {
		t.Fatalf("CheckVersion(%d) = %v", ProtocolVersion, err)
	}
	if err := CheckVersion(ProtocolVersion - 1); !errors.Is(err, ErrProtocolVersion) {
		t.Fatalf("CheckVersion refused a mismatched version with %v, want ErrProtocolVersion", err)
	}
}

// frameWithHeader builds a header-only frame from raw header bytes.
func frameWithHeader(header []byte) io.Reader {
	var buf bytes.Buffer
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(header)))
	buf.Write(length[:])
	buf.Write(header)
	buf.Write([]byte{0, 0, 0, 0})
	return &buf
}

// frameHeaderPrefix builds the first half of a frame (a valid header with its
// length prefix) so a test can append an arbitrary body length.
func frameHeaderPrefix(header string) []byte {
	out := make([]byte, 4, 8+len(header))
	binary.BigEndian.PutUint32(out, uint32(len(header)))
	out = append(out, header...)
	return out
}
