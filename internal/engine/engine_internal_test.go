package engine

// White-box tests for the parts of engine.go that are not reachable from the
// public surface. The shutdown-message parser is one of them: it reads a
// message shape the durable runtime owns, so its behaviour on a shape it does
// not expect must be pinned directly.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestParsePendingShutdownComponents pins how the parser reads the runtime's
// message. The runtime names the parts that were still running only in that
// text, so the parser is the whole contract: it must read every shape the
// runtime can write, and it must give up cleanly rather than invent a part.
func TestParsePendingShutdownComponents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   error
		want []ShutdownComponent
	}{
		{
			name: "no error at all",
			in:   nil,
			want: nil,
		},
		{
			name: "a failure that is not a timeout carries no marker",
			in:   errors.New("system database is closed"),
			want: nil,
		},
		{
			name: "one part",
			in:   fmt.Errorf("shutdown timed out after 1s waiting for: workflows"),
			want: []ShutdownComponent{ShutdownComponentWorkflows},
		},
		{
			name: "several parts, the shape the runtime writes today",
			in:   fmt.Errorf("shutdown timed out after 1s waiting for: queue runner, workflows, system database connection pool"),
			want: []ShutdownComponent{
				ShutdownComponentQueueRunner,
				ShutdownComponentWorkflows,
				ShutdownComponentConnectionPool,
			},
		},
		{
			name: "several parts without the space after the comma",
			in:   fmt.Errorf("shutdown timed out after 1s waiting for: queue runner,workflows"),
			want: []ShutdownComponent{ShutdownComponentQueueRunner, ShutdownComponentWorkflows},
		},
		{
			name: "a trailing separator adds no empty part",
			in:   fmt.Errorf("shutdown timed out after 1s waiting for: workflows, "),
			want: []ShutdownComponent{ShutdownComponentWorkflows},
		},
		{
			name: "the marker with nothing after it",
			in:   fmt.Errorf("shutdown timed out after 1s waiting for: "),
			want: nil,
		},
		{
			name: "a part this build does not know is kept, not dropped",
			in:   fmt.Errorf("shutdown timed out after 1s waiting for: workflows, telemetry exporter"),
			want: []ShutdownComponent{ShutdownComponentWorkflows, ShutdownComponent("telemetry exporter")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parsePendingShutdownComponents(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("parsed %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("part %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestShutdownIncompleteError_DegradesWithoutLosingTheCause pins the degrade
// path: when the runtime reports a failure this build cannot read the parts
// from, the caller still gets the runtime's own words. Silence there would
// leave an operator with a failure and no account of it.
func TestShutdownIncompleteError_DegradesWithoutLosingTheCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("the database went away while stopping")
	detail := newShutdownIncomplete(3*time.Second, cause)

	if len(detail.Pending) != 0 {
		t.Errorf("Pending = %v, want it empty for a message with no part list", detail.Pending)
	}
	if !errors.Is(detail, cause) {
		t.Error("the runtime's own error is no longer reachable from the reported error")
	}
	msg := detail.Error()
	for _, want := range []string{cause.Error(), "3s", "did not name which parts"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not carry %q", msg, want)
		}
	}
}
