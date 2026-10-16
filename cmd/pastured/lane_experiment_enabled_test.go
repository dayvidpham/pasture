//go:build pasture_experiment

package main

import (
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestExperimentBudgetRequiresPrivateSandboxAndValidBounds(t *testing.T) {
	root, err := os.MkdirTemp("/tmp/opencode", "lane-budget-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	db := filepath.Join(root, "store.db")
	if err := os.WriteFile(db, nil, 0600); err != nil {
		t.Fatal(err)
	}
	base, err := writelane.BudgetFromProfile(timeouts.ProductionProfile())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASTURE_EXPERIMENT_LANE_BUDGET", "")
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", "")
	got, err := laneExperimentBudget(base, db)
	if err != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("experiment build changed defaults without opt-in")
	}
	t.Setenv("PASTURE_EXPERIMENT_SANDBOX", root)
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", filepath.Join(root, "metrics.jsonl"))
	t.Setenv("PASTURE_EXPERIMENT_LANE_BUDGET", `{"batch_records":40,"flush_ms":100,"queue_records":64,"batch_bytes":1048576,"queue_bytes":2097152}`)
	got, err = laneExperimentBudget(base, db)
	if err != nil || got.BatchRecords != 40 || got.FlushInterval != 100*time.Millisecond || got.QueueRecords != 64 || got.BatchBytes != 1048576 || got.QueueBytes != 2097152 {
		t.Fatalf("valid experiment not wired: %+v %v", got, err)
	}
	if _, err := laneExperimentBudget(base, filepath.Join(t.TempDir(), "outside.db")); err == nil {
		t.Fatal("outside database accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.db")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := laneExperimentBudget(base, link); err == nil {
		t.Fatal("symlink escape accepted")
	}
	dangling := filepath.Join(root, "dangling.jsonl")
	if err := os.Symlink(filepath.Join(t.TempDir(), "absent.jsonl"), dangling); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", dangling)
	if _, err := laneExperimentBudget(base, db); err == nil {
		t.Fatal("dangling output symlink accepted")
	}
	alias := filepath.Join(root, "alias.jsonl")
	if err := os.Link(db, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{db, db + "-wal", db + "-shm", db + "-journal", alias} {
		t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", path)
		if _, err := laneExperimentBudget(base, db); err == nil {
			t.Fatalf("database/alias output accepted: %s", path)
		}
	}
	parentAlias := filepath.Join(root, "parent-alias")
	if err := os.Symlink(root, parentAlias); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		aliased := filepath.Join(parentAlias, "store.db"+suffix)
		t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", aliased)
		if _, err := laneExperimentBudget(base, db); err == nil {
			t.Fatal("parent-directory alias bypassed sidecar reservation")
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(cwd, aliased)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", relative)
		if _, err := laneExperimentBudget(base, db); err == nil {
			t.Fatal("relative sidecar spelling accepted")
		}
	}
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", filepath.Join(root, "metrics.jsonl"))
	for _, raw := range []string{`{"unknown":1}`, `{"batch_records":300}`, `{"queue_records":1}`, `{"flush_ms":400}`, `{"batch_bytes":1}`, `{} {}`} {
		t.Setenv("PASTURE_EXPERIMENT_LANE_BUDGET", raw)
		if _, err := laneExperimentBudget(base, db); err == nil {
			t.Fatalf("invalid bounds accepted: %s", raw)
		}
	}
}
