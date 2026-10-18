//go:build !pasture_experiment

package main

import (
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/writelane"
)

func laneExperimentBudget(base writelane.WriteLaneBudget, _ string) (writelane.WriteLaneBudget, error) {
	if os.Getenv("PASTURE_EXPERIMENT_LANE_BUDGET") != "" || os.Getenv("PASTURE_EXPERIMENT_LANE_METRICS") != "" {
		return base, fmt.Errorf("lane experiment inputs require a binary built with -tags=pasture_experiment; normal builds never apply these settings")
	}
	return base, nil
}
