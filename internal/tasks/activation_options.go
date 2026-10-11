//go:build !provenance_testhooks

package tasks

import "github.com/dayvidpham/provenance"

func borrowedActivationOptions(cfg openTaskTrackerOptions) []provenance.Option {
	return []provenance.Option{provenance.WithBorrowedActivationBusyTimeout(cfg.timeouts.SQLiteBusy())}
}
