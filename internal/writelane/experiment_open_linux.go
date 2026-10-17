//go:build pasture_experiment && linux

package writelane

import (
	"os"
	"syscall"
)

func openExperimentMetrics(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
}
