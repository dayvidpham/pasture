// Package lanewire is the wire protocol between the lifecycle hook and the
// daemon's write lane. It is shared by both ends on purpose: one package owns
// the frame shape, the protocol version and the strict decode, so the two
// processes cannot drift into a frame one of them writes and the other cannot
// read.
//
// The frame is length-prefixed with a strict JSON header and a raw payload
// body, because the payload is the host's bytes verbatim and must never be
// re-encoded through JSON: a hook that re-encoded its stdin could change the
// evidence the receipt hashes.
package lanewire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is the version of this frame shape and its two header types.
// It is carried in BOTH directions and checked by BOTH ends: an old hook
// against a new daemon and a new hook against an old daemon must each fall back
// to the direct write path rather than misinterpret a frame.
const ProtocolVersion = 1

// Response kinds. Exactly one is set on every well-formed response.
const (
	// KindCommitted means the occurrence is durable in the store before this
	// response was written. The host may be told "proceed" only for this kind.
	KindCommitted = "committed"
	// KindRefused means nothing, or nothing further, was committed: the refusal
	// carries the stage that says which.
	KindRefused = "refused"
)

// MaxHeaderBytes bounds the JSON header. A header larger than this is refused
// before it is decoded, so a hostile or broken peer cannot make the daemon
// allocate without bound.
const MaxHeaderBytes = 64 << 10

// MaxBodyBytes bounds the payload body. It is the ingress payload cap plus
// headroom, never unbounded.
const MaxBodyBytes = 4 << 20

// ErrProtocolVersion marks a frame whose version is not ProtocolVersion. It is
// a fallback signal, not a fault: the hook answers the host through the direct
// write path exactly as it does when no daemon is reachable.
var ErrProtocolVersion = errors.New("lifecycle write lane protocol version mismatch")

// ErrFrameTooLarge marks a declared header or body length above its bound.
var ErrFrameTooLarge = errors.New("lifecycle write lane frame exceeds its bound")

// Request is one "consult and record" request: the hook's coordinates, its
// version observation, its session claim, and the payload body in the frame.
//
// IT CARRIES NO DECISION THE DAEMON MUST TRUST. Everything the daemon needs to
// re-validate the delivery is here or in the body: the daemon re-runs the
// ingress parse, re-resolves the event, re-reads the gate and takes its own
// verdict. A hook cannot smuggle a verdict, a receipt, or an occurrence
// identity into this request, so a compromised hook can at worst ask the same
// question the host asked.
type Request struct {
	Version           int    `json:"version"`
	Harness           string `json:"harness"`
	Event             string `json:"event"`
	HostVersion       string `json:"host_version"`
	HostVersionSource string `json:"host_version_source,omitempty"`
	// ActorClaim is the actor the host session claims, empty for no claim.
	ActorClaim string `json:"actor_claim,omitempty"`
}

// Response is one answer. Kind tells the two apart; the rest is filled
// according to the kind, and strict decoding refuses a member belonging to the
// other kind so a malformed answer is a fallback rather than a misread.
type Response struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	// Committed fields. ExitStatus is the DECLARED hostexit.ExitStatus value
	// (its enum member, not the process code it maps to), so both ends validate
	// the same closed set instead of translating codes.
	ExitStatus   int    `json:"exit_status,omitempty"`
	Stdout       []byte `json:"stdout,omitempty"`
	Stderr       string `json:"stderr,omitempty"`
	OccurrenceID string `json:"occurrence_id,omitempty"`
	ClaimState   string `json:"claim_state,omitempty"`
	// Refused fields. Stage is a hostexit.FaultStage name: what pasture knows
	// about the durable state of the event. Refusal is the cause text the hook
	// renders into its own diagnostic, because the failure policy and the
	// continuation bytes are the HOOK's, not the daemon's.
	Stage   string `json:"stage,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// WriteMessage writes one frame: a big-endian u32 header length, the header
// JSON, a big-endian u32 body length, and the body bytes.
func WriteMessage(w io.Writer, header any, body []byte) error {
	encoded, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("encode the lifecycle write lane frame header: %w", err)
	}
	if len(encoded) > MaxHeaderBytes {
		return fmt.Errorf("%w: the encoded header is %d bytes, above the %d-byte bound", ErrFrameTooLarge, len(encoded), MaxHeaderBytes)
	}
	if len(body) > MaxBodyBytes {
		return fmt.Errorf("%w: the payload body is %d bytes, above the %d-byte bound", ErrFrameTooLarge, len(body), MaxBodyBytes)
	}
	frame := make([]byte, 0, 8+len(encoded)+len(body))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(encoded)))
	frame = append(frame, encoded...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(body)))
	frame = append(frame, body...)
	for len(frame) > 0 {
		written, err := w.Write(frame)
		if err != nil {
			return fmt.Errorf("write the lifecycle write lane frame: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("write the lifecycle write lane frame: the stream accepted no bytes; the connection is broken")
		}
		frame = frame[written:]
	}
	return nil
}

// ReadMessage reads one frame and decodes its strict JSON header into header.
// It returns the raw body bytes.
//
// THE HEADER IS DECODED STRICTLY, and the body is not decoded at all: an
// unknown member is a refusal rather than a silently ignored field, and a
// trailing token after the JSON object is a refusal rather than a second
// message nobody reads.
func ReadMessage(r io.Reader, header any) ([]byte, error) {
	headerLength, err := readUint32(r)
	if err != nil {
		return nil, fmt.Errorf("read the lifecycle write lane header length: %w", err)
	}
	if headerLength > MaxHeaderBytes {
		return nil, fmt.Errorf("%w: the declared header is %d bytes, above the %d-byte bound", ErrFrameTooLarge, headerLength, MaxHeaderBytes)
	}
	encoded := make([]byte, headerLength)
	if _, err := io.ReadFull(r, encoded); err != nil {
		return nil, fmt.Errorf("read the lifecycle write lane header: %w", err)
	}
	decoder := json.NewDecoder(newByteReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(header); err != nil {
		return nil, fmt.Errorf("decode the lifecycle write lane header: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode the lifecycle write lane header: the JSON object is followed by more data, so the header cannot be trusted")
	}

	bodyLength, err := readUint32(r)
	if err != nil {
		return nil, fmt.Errorf("read the lifecycle write lane body length: %w", err)
	}
	if bodyLength > MaxBodyBytes {
		return nil, fmt.Errorf("%w: the declared body is %d bytes, above the %d-byte bound", ErrFrameTooLarge, bodyLength, MaxBodyBytes)
	}
	body := make([]byte, bodyLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("read the lifecycle write lane body: %w", err)
	}
	return body, nil
}

// checkVersion refuses a version that is not this build's. It is exported so
// both endpoints apply the SAME check at the SAME point rather than each
// remembering to.
func CheckVersion(version int) error {
	if version != ProtocolVersion {
		return fmt.Errorf("%w: the peer speaks version %d and this build speaks %d; the hook falls back to the direct write path", ErrProtocolVersion, version, ProtocolVersion)
	}
	return nil
}

func readUint32(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

// byteReader is an io.Reader over a byte slice without importing bytes; it
// keeps the strict-decode reader local and allocation-free.
type byteReader struct {
	data []byte
	at   int
}

func newByteReader(data []byte) *byteReader { return &byteReader{data: data} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.at >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.at:])
	r.at += n
	return n, nil
}
