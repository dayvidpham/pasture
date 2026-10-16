//go:build pasture_experiment

package writelane

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/dayvidpham/provenance"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExperimentMetricsObserveRealQueueAndTimerFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", path)
	lane, err := New(WriteLaneBudget{QueueRecords: 4, QueueBytes: 2 << 20, BatchRecords: 2, BatchBytes: 1 << 20, FlushInterval: time.Millisecond, HoldTarget: time.Second}, func(_ context.Context, in []provenance.OperationInput) (provenance.BatchResult, error) {
		return provenance.BatchResult{Records: []provenance.BatchRecordResult{{Outcome: provenance.BatchRecordCommitted}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lane.Submit(context.Background(), 123, commitFunc(func(context.Context) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := lane.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Reason          string `json:"reason"`
		Records         int    `json:"records"`
		Bytes           int    `json:"bytes"`
		MaxQueueRecords int    `json:"max_queue_records"`
		MaxQueueBytes   int    `json:"max_queue_bytes"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Reason != "timer" || event.Records != 1 || event.Bytes != 123 || event.MaxQueueRecords != 1 || event.MaxQueueBytes != 123 {
		t.Fatalf("metrics do not describe the actual admission/flush: %s", raw)
	}
}

func TestExperimentStagedFlushReasonsAndHighWater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", path)
	lane, err := New(WriteLaneBudget{QueueRecords: 4, QueueBytes: 2 << 20, BatchRecords: 2, BatchBytes: 1 << 20, FlushInterval: time.Millisecond, HoldTarget: time.Second}, func(_ context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error) {
		result := provenance.BatchResult{Committed: true}
		for range inputs {
			result.Records = append(result.Records, provenance.BatchRecordResult{Outcome: provenance.BatchRecordCommitted})
		}
		return result, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lane.Close(context.Background()) })
	completed := coalesce(t, lane, []Record{&realRecord{}, &realRecord{}, &realRecord{}, &realRecord{}})
	for _, done := range completed {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := lane.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]int{}
	records, maxQueue, maxBytes := 0, 0, 0
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var event struct {
			Reason   string `json:"reason"`
			Records  int    `json:"records"`
			MaxQueue int    `json:"max_queue_records"`
			MaxBytes int    `json:"max_queue_bytes"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		reasons[event.Reason]++
		records += event.Records
		if event.MaxQueue > maxQueue {
			maxQueue = event.MaxQueue
		}
		if event.MaxBytes > maxBytes {
			maxBytes = event.MaxBytes
		}
	}
	if records != 5 || reasons["full"] != 1 || reasons["drained"] != 1 || maxQueue != 4 || maxBytes != 32 {
		t.Fatalf("flush accounting records=%d reasons=%v queue=%d bytes=%d", records, reasons, maxQueue, maxBytes)
	}
}

func TestExperimentOutputFailureIsExplicitAndDoesNotChangeACK(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", dir)
	lane, err := New(WriteLaneBudget{QueueRecords: 4, QueueBytes: 2 << 20, BatchRecords: 2, BatchBytes: 1 << 20, FlushInterval: time.Millisecond, HoldTarget: time.Second}, func(context.Context, []provenance.OperationInput) (provenance.BatchResult, error) {
		return provenance.BatchResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lane.Submit(context.Background(), 1, commitFunc(func(context.Context) error { return nil })); err != nil {
		t.Fatalf("instrumentation changed durable ACK: %v", err)
	}
	if err := lane.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lane.experimentMetricErrors.Load() != 1 {
		t.Fatal("failed observation was silently discarded")
	}
}

func TestExperimentOutputNeverFollowsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "absent.jsonl")
	link := filepath.Join(dir, "out.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	f, err := openExperimentMetrics(link)
	if err == nil {
		_ = f.Close()
		t.Fatal("output open followed a symlink")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("outside target was created")
	}
}
