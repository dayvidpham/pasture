//go:build pasture_experiment && !linux

package writelane

import (
	"fmt"
	"os"
)

func openExperimentMetrics(string) (*os.File, error) {
	return nil, fmt.Errorf("lane experiment metrics are supported only on the Linux measurement host; use a normal build elsewhere")
}
