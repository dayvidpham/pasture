package formatters_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
)

// TestFormatQueueConcurrency covers the three renderings an operator can meet:
// a limit, no limit at all, and an output format pasture does not know.
//
// "No limit" must not render as a number. A queue with no limit runs as many
// jobs as it can dequeue, which is a different operational state from any
// number, and printing "0" for it would tell the operator the opposite of the
// truth.
func TestFormatQueueConcurrency(t *testing.T) {
	t.Parallel()
	limit := 8

	tests := []struct {
		name        string
		in          formatters.QueueConcurrency
		format      types.OutputFormat
		wantErr     bool
		wantText    string
		wantJSONNum *int
	}{
		{
			name:     "text with a limit",
			in:       formatters.QueueConcurrency{Queue: "pasture-slice-queue", WorkerConcurrency: &limit},
			format:   types.OutputText,
			wantText: "pasture-slice-queue: 8 concurrent jobs per process",
		},
		{
			name:     "text with no limit",
			in:       formatters.QueueConcurrency{Queue: "pasture-slice-queue"},
			format:   types.OutputText,
			wantText: "pasture-slice-queue: no limit on concurrent jobs per process",
		},
		{
			name:        "json with a limit",
			in:          formatters.QueueConcurrency{Queue: "pasture-slice-queue", WorkerConcurrency: &limit},
			format:      types.OutputJSON,
			wantJSONNum: &limit,
		},
		{
			name:   "json with no limit",
			in:     formatters.QueueConcurrency{Queue: "pasture-slice-queue"},
			format: types.OutputJSON,
		},
		{
			name:    "unknown format",
			in:      formatters.QueueConcurrency{Queue: "pasture-slice-queue", WorkerConcurrency: &limit},
			format:  types.OutputFormat("yaml"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := formatters.FormatQueueConcurrency(tc.in, tc.format)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("FormatQueueConcurrency returned %q with no error; want an error", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("FormatQueueConcurrency: %v", err)
			}
			if tc.wantText != "" {
				if out != tc.wantText {
					t.Errorf("output = %q, want %q", out, tc.wantText)
				}
				return
			}
			var decoded struct {
				Queue             string `json:"queue"`
				WorkerConcurrency *int   `json:"worker_concurrency"`
			}
			if err := json.Unmarshal([]byte(out), &decoded); err != nil {
				t.Fatalf("output is not valid JSON (%v): %s", err, out)
			}
			if decoded.Queue != tc.in.Queue {
				t.Errorf("queue = %q, want %q", decoded.Queue, tc.in.Queue)
			}
			switch {
			case tc.wantJSONNum == nil && decoded.WorkerConcurrency != nil:
				t.Errorf("worker_concurrency = %d, want null (no limit)", *decoded.WorkerConcurrency)
			case tc.wantJSONNum != nil && decoded.WorkerConcurrency == nil:
				t.Errorf("worker_concurrency = null, want %d", *tc.wantJSONNum)
			case tc.wantJSONNum != nil && *decoded.WorkerConcurrency != *tc.wantJSONNum:
				t.Errorf("worker_concurrency = %d, want %d", *decoded.WorkerConcurrency, *tc.wantJSONNum)
			}
			if !strings.Contains(out, "worker_concurrency") {
				t.Errorf("JSON output omits worker_concurrency: %s", out)
			}
		})
	}
}
