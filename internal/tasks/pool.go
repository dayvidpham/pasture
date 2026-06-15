// Package tasks — pool.go defines the PASTURE_DB_POOL_SIZE production env knob
// for the pasture-owned audit handle.
//
// This governs ONLY the audit handle opened by openAuditHandle in
// open_unified.go (used for AttachContext, EventContexts, SetAgentCategories,
// and Timeline). It does NOT affect:
//   - The audit-trail handle (internal/audit/sqlite.go) used by RecordEvent.
//   - The provenance handle opened by provenance.OpenSQLite.
//
// The default pool size of 1 serialises writes at the Go level, which is the
// proven-safe configuration: zero escaped SQLITE_BUSY errors in the
// cross-subsystem race test. SQLite WAL + busy_timeout=5000 prevent data
// corruption at any pool size; the default of 1 is a contention-serialisation
// choice, not a correctness requirement. A pool > 1 raises write concurrency
// but may surface SQLITE_BUSY under heavy contention on the pasture-owned
// handle.

package tasks

import (
	"fmt"
	"os"
	"strconv"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// DBPoolSizeEnv is the environment variable that overrides the connection-pool
// size for the pasture-owned audit handle. When set, its integer value is used
// instead of DefaultDBPoolSize.
//
// This is a production tuning knob. Tests should leave it unset (default 1).
const DBPoolSizeEnv = "PASTURE_DB_POOL_SIZE"

// DefaultDBPoolSize is the default connection-pool size for the pasture-owned
// audit handle.
//
// A pool of 1 serialises writes at the Go level and is the proven-safe default:
// zero escaped SQLITE_BUSY errors under the cross-subsystem race test. SQLite
// WAL + busy_timeout=5000 prevent data corruption at any pool size; this cap is
// a contention-serialisation choice only.
//
// For production tuning, override with PASTURE_DB_POOL_SIZE after validating
// your storage throughput. A higher pool raises write concurrency but may
// surface SQLITE_BUSY under heavy contention.
const DefaultDBPoolSize = 1

// ResolveDBPoolSize resolves the effective connection-pool size for the
// pasture-owned audit handle from two sources, highest-priority first:
//
//  1. $PASTURE_DB_POOL_SIZE (non-empty, parses as a positive int).
//  2. DefaultDBPoolSize (1).
//
// If the env var is set but not a valid positive integer, the function returns
// an actionable validation error (the caller should surface it and exit 1).
//
// Tradeoff: the default of 1 is the proven-safe pool size (zero escaped
// SQLITE_BUSY in the cross-subsystem race test). A pool > 1 raises write
// concurrency on the pasture-owned handle but may surface SQLITE_BUSY under
// heavy contention. SQLite WAL + busy_timeout=5000 prevent data corruption at
// any pool size; this setting is purely about contention serialisation.
//
// This governs ONLY the pasture-owned audit handle (AttachContext, EventContexts,
// SetAgentCategories, Timeline). The audit-trail handle (RecordEvent) and the
// provenance handle each manage their own connection pool and are not affected.
func ResolveDBPoolSize() (int, error) {
	envStr := os.Getenv(DBPoolSizeEnv)
	if envStr != "" {
		v, err := strconv.Atoi(envStr)
		if err != nil || v <= 0 {
			return 0, &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("$%s=%q is not a valid pool size.", DBPoolSizeEnv, envStr),
				Why:      "The environment variable must be a positive integer.",
				Where:    "Resolving the audit-handle connection-pool size (internal/tasks/pool.go in tasks.ResolveDBPoolSize).",
				Impact:   "The pasture database cannot be opened without a valid pool size for the audit handle.",
				Fix: fmt.Sprintf(
					"Set $%s to a positive integer (default is %d):\n"+
						"  export %s=1\n"+
						"Or unset it to use the safe default:\n"+
						"  unset %s\n"+
						"Note: the default of 1 serialises writes at the Go level (zero escaped SQLITE_BUSY);\n"+
						"increasing the pool raises write concurrency but may surface SQLITE_BUSY\n"+
						"under heavy contention on the pasture-owned audit handle (not corruption —\n"+
						"SQLite WAL + busy_timeout=5000 prevent data corruption at any pool size).",
					DBPoolSizeEnv, DefaultDBPoolSize, DBPoolSizeEnv, DBPoolSizeEnv,
				),
			}
		}
		return v, nil
	}
	return DefaultDBPoolSize, nil
}
