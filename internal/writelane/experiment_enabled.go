//go:build pasture_experiment

package writelane

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Queue observation runs under l.mu, after admission, before any consumer can
// remove the record. Bytes are staged payload accounting, not total Go heap.
func (l *Lane) observeExperimentQueue() {
	if os.Getenv("PASTURE_EXPERIMENT_LANE_METRICS") == "" {
		return
	}
	raiseMax(&l.experimentQueueRecords, int64(len(l.pending)))
	raiseMax(&l.experimentQueueBytes, l.pendingBytes)
}

func (l *Lane) flushBatch(batch []*item, reason string) {
	output := os.Getenv("PASTURE_EXPERIMENT_LANE_METRICS")
	if output == "" {
		l.commitBatch(batch)
		return
	}
	started := time.Now()
	l.commitBatch(batch)
	elapsed := time.Since(started)
	bytes := int64(0)
	for _, item := range batch {
		bytes += item.bytes
	}
	// Output is AFTER commit/settlement and outside the SQLite transaction.
	// Its possible effect on the next turn is measured by the off/on controls.
	f, err := openExperimentMetrics(output)
	if err != nil {
		l.experimentMetricsError(err)
		return
	}
	err = json.NewEncoder(f).Encode(struct {
		Reason          string          `json:"reason"`
		Records         int             `json:"records"`
		Bytes           int64           `json:"bytes"`
		Full            bool            `json:"full_condition"`
		TurnNanoseconds int64           `json:"turn_elapsed_ns"`
		MaxQueueRecords int64           `json:"max_queue_records"`
		MaxQueueBytes   int64           `json:"max_queue_bytes"`
		Budget          WriteLaneBudget `json:"budget"`
	}{reason, len(batch), bytes, ExceedsWriteBatch(l.budget, len(batch), bytes, 0), elapsed.Nanoseconds(), l.experimentQueueRecords.Load(), l.experimentQueueBytes.Load(), l.budget})
	if err != nil {
		l.experimentMetricsError(err)
	}
	if err := f.Close(); err != nil {
		l.experimentMetricsError(err)
	}
}

func (l *Lane) experimentMetricsError(err error) {
	l.experimentMetricErrors.Add(1)
	fmt.Fprintf(os.Stderr, "EXPERIMENT_METRICS_ERROR: lane observation output failed after settlement: %v; discard this campaign's metric comparisons, committed outcomes are unchanged\n", err)
}
