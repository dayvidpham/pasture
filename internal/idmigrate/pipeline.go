package idmigrate

import (
	"context"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// CatchUpOptions configures the shared P1+P2 orchestration.
type CatchUpOptions struct {
	// StorePath is the store the pipeline converts.
	StorePath string
	// BackupDir overrides where P1 writes the consistent archive. Empty uses the
	// store's own directory.
	BackupDir string
	// Now is the timestamp source for the archive name. Nil means the wall clock.
	Now func() time.Time
	// TimeoutProfile, when non-zero, is applied to the audit catch-up trail. The
	// offline command leaves it zero (the audit subsystem's own default); the
	// first-open auto cutover passes the resolved store profile.
	TimeoutProfile timeouts.Profile
}

// CatchUpResult is the P1+P2 state both frontends hand to Convert.
type CatchUpResult struct {
	// Archive is the P1 result (its Path is empty when P1 skipped a store that
	// did not exist).
	Archive Archive
	// AppliedCatchUpSteps is how many audit steps P2 applied this run.
	AppliedCatchUpSteps int
	// Trail is the catch-up audit trail, left OPEN and current: the first-open
	// auto cutover reuses it as the tracker's trail, and the offline command
	// closes it immediately after the catch-up.
	Trail *audit.SqliteAuditTrail
}

// RunCatchUp is the single P1+P2 orchestration both frontends call, so they can
// never diverge on the order or on how the applied-step count is computed:
//
//	P1 archive (ArchiveStore): a consistent copy through a read-only source,
//	   skipped only when the store did not exist at P1's own read-only check.
//	P2 audit catch-up with the DEFERRED identity-format ceiling, so a populated
//	   legacy store stops at the cutover target and the conversion applies the
//	   remaining ceiling step atomically with the marker and the receipt.
//
// Order is load-bearing: the archive precedes ANY write, and the catch-up
// precedes the conversion. The returned trail is left open; the caller owns
// closing it.
func RunCatchUp(ctx context.Context, opts CatchUpOptions) (CatchUpResult, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	archive, err := ArchiveStore(ctx, opts.StorePath, opts.BackupDir, now())
	if err != nil {
		return CatchUpResult{}, err
	}

	beforeVersion := RecordedSchemaVersionPath(opts.StorePath)
	trailOptions := []audit.SqliteAuditTrailOption{audit.WithDeferredCeiling(audit.MaxKnownSchemaVersion - 1)}
	if !opts.TimeoutProfile.IsZero() {
		trailOptions = append(trailOptions, audit.WithTimeoutProfile(opts.TimeoutProfile))
	}
	trail, err := audit.NewSqliteAuditTrailContextWithOptions(ctx, opts.StorePath, trailOptions...)
	if err != nil {
		return CatchUpResult{}, err
	}
	applied := RecordedSchemaVersionPath(opts.StorePath) - beforeVersion
	if applied < 0 {
		applied = 0
	}
	return CatchUpResult{Archive: archive, AppliedCatchUpSteps: applied, Trail: trail}, nil
}
