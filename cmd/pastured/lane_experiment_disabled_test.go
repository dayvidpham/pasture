//go:build !pasture_experiment

package main

import (
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"reflect"
	"testing"
)

func TestNormalBuildNeverAppliesExperimentBudget(t *testing.T) {
	base, err := writelane.BudgetFromProfile(timeouts.ProductionProfile())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASTURE_EXPERIMENT_LANE_BUDGET", "")
	t.Setenv("PASTURE_EXPERIMENT_LANE_METRICS", "")
	got, err := laneExperimentBudget(base, "unused")
	if err != nil || !reflect.DeepEqual(got, base) {
		t.Fatalf("default changed: %+v %v", got, err)
	}
	t.Setenv("PASTURE_EXPERIMENT_LANE_BUDGET", `{"batch_records":1}`)
	if _, err := laneExperimentBudget(base, "unused"); err == nil {
		t.Fatal("normal binary accepted experimental knobs")
	}
}
