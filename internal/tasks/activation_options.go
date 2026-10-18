//go:build !provenance_testhooks

package tasks

import "github.com/dayvidpham/provenance"

func borrowedActivationOptions(cfg openTaskTrackerOptions) []provenance.Option {
	options := []provenance.Option{provenance.WithBorrowedActivationBusyTimeout(cfg.timeouts.SQLiteBusy())}
	if cfg.modelRegistry != nil {
		options = append(options, provenance.WithModelRegistry(cfg.modelRegistry))
	}
	return options
}
