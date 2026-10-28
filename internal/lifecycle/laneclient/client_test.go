package laneclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
)

// serveOnce accepts one connection and runs handle against it.
func serveOnce(t *testing.T, socketPath string, handle func(conn net.Conn)) (net.Listener, chan struct{}) {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on %s: %v", socketPath, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}()
	return listener, done
}

func acceptProbe(t *testing.T, conn net.Conn) {
	t.Helper()
	var probe lanewire.Request
	body, err := lanewire.ReadMessage(conn, &probe)
	if err != nil || probe.Version != lanewire.ProtocolVersion || probe.Harness != "" || len(body) != 0 {
		t.Errorf("invalid version probe: %+v, %v", probe, err)
		return
	}
	if err := lanewire.WriteMessage(conn, lanewire.Response{Version: lanewire.ProtocolVersion, Kind: lanewire.KindReady}, nil); err != nil {
		t.Errorf("write probe answer: %v", err)
	}
}

func TestConsultReturnsACommittedAnswer(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "lane.sock")
	listener, done := serveOnce(t, socketPath, func(conn net.Conn) {
		acceptProbe(t, conn)
		var request lanewire.Request
		body, err := lanewire.ReadMessage(conn, &request)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if string(body) != "payload" {
			t.Errorf("server body = %q", body)
		}
		if request.Harness != "opencode" || request.Version != lanewire.ProtocolVersion {
			t.Errorf("server request = %+v", request)
		}
		_ = lanewire.WriteMessage(conn, lanewire.Response{
			Version: lanewire.ProtocolVersion, Kind: lanewire.KindCommitted,
			ExitStatus: 1, Stdout: []byte("{}"), OccurrenceID: "occ-1",
		}, nil)
	})
	defer listener.Close()

	client := New(socketPath, time.Second)
	response, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
	if err != nil {
		t.Fatalf("Consult returned %v", err)
	}
	if response.Kind != lanewire.KindCommitted || response.OccurrenceID != "occ-1" {
		t.Fatalf("response = %+v", response)
	}
	<-done
}

func TestConsultReportsUnavailableWhenTheSocketIsAbsent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	client := New(socketPath, 200*time.Millisecond)
	_, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Consult against an absent socket returned %v, want ErrUnavailable", err)
	}
	if errors.Is(err, ErrRecordUnknown) {
		t.Fatalf("an unreachable daemon was reported as record-unknown: %v", err)
	}
}

func TestConsultReportsRecordUnknownWhenTheAnswerNeverComes(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "lane.sock")
	listener, done := serveOnce(t, socketPath, func(conn net.Conn) {
		acceptProbe(t, conn)
		// Read the request and close without answering: the daemon may have
		// committed it, so the hook must not claim not-recorded.
		var request lanewire.Request
		if _, err := lanewire.ReadMessage(conn, &request); err != nil {
			t.Errorf("server read: %v", err)
		}
	})
	defer listener.Close()

	client := New(socketPath, time.Second)
	_, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
	if !errors.Is(err, ErrRecordUnknown) {
		t.Fatalf("Consult with no answer returned %v, want ErrRecordUnknown", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("a sent request was classified as provably nothing-sent: %v", err)
	}
	<-done
}

func TestConsultFallsBackOnAProtocolMismatch(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "lane.sock")
			listener, done := serveOnce(t, socketPath, func(conn net.Conn) {
				var request lanewire.Request
				body, err := lanewire.ReadMessage(conn, &request)
				if err != nil || request.Harness != "" || len(body) != 0 {
					t.Errorf("old daemon received receipt work during probe: %+v body=%q err=%v", request, body, err)
				}
				_ = lanewire.WriteMessage(conn, lanewire.Response{Version: version, Kind: lanewire.KindRefused}, nil)
			})
			defer listener.Close()

			client := New(socketPath, time.Second)
			_, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("a protocol mismatch returned %v, want ErrUnavailable so the hook writes directly", err)
			}
			<-done
		})
	}
}

func TestConsultNeverFallsBackAfterReceiptOnVersionMismatch(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "lane.sock")
	listener, done := serveOnce(t, socketPath, func(conn net.Conn) {
		acceptProbe(t, conn)
		var request lanewire.Request
		body, err := lanewire.ReadMessage(conn, &request)
		if err != nil || string(body) != "payload" {
			t.Errorf("receipt request: body=%q err=%v", body, err)
			return
		}
		_ = lanewire.WriteMessage(conn, lanewire.Response{Version: 1, Kind: lanewire.KindCommitted}, nil)
	})
	defer listener.Close()
	client := New(socketPath, time.Second)
	_, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
	if !errors.Is(err, ErrRecordUnknown) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("post-send mismatch = %v, want record-unknown only", err)
	}
	<-done
}

func TestConsultRefusesAnUnknownAnswerKind(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "lane.sock")
	listener, done := serveOnce(t, socketPath, func(conn net.Conn) {
		acceptProbe(t, conn)
		var request lanewire.Request
		_, _ = lanewire.ReadMessage(conn, &request)
		_ = lanewire.WriteMessage(conn, lanewire.Response{Version: lanewire.ProtocolVersion, Kind: "maybe"}, nil)
	})
	defer listener.Close()

	client := New(socketPath, time.Second)
	_, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
	if !errors.Is(err, ErrRecordUnknown) {
		t.Fatalf("an unrecognised answer kind returned %v, want ErrRecordUnknown", err)
	}
	<-done
}

func TestConsultReturnsARefusedAnswer(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "lane.sock")
	listener, done := serveOnce(t, socketPath, func(conn net.Conn) {
		acceptProbe(t, conn)
		var request lanewire.Request
		_, _ = lanewire.ReadMessage(conn, &request)
		_ = lanewire.WriteMessage(conn, lanewire.Response{
			Version: lanewire.ProtocolVersion, Kind: lanewire.KindRefused,
			Stage: "not-recorded", Refusal: "the event is withheld",
		}, nil)
	})
	defer listener.Close()

	client := New(socketPath, time.Second)
	response, err := client.Consult(context.Background(), lanewire.Request{Harness: "opencode"}, []byte("payload"))
	if err != nil {
		t.Fatalf("Consult returned %v", err)
	}
	if response.Kind != lanewire.KindRefused || response.Stage != "not-recorded" {
		t.Fatalf("response = %+v", response)
	}
	<-done
}
