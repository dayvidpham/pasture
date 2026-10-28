package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/provadapter"
)

const defaultLLMInitializationVersion = 1

// ensureDefaultLLMActor checks the versioned marker without taking a write
// lock on the initialized path. The fixed registrar owns its own transaction;
// the marker is a separate, last write, so a crash between the two converges
// through an exact idempotent registration on the next normal open.
func ensureDefaultLLMActor(ctx context.Context, db *sql.DB, tr provenance.Tracker, hook bool) error {
	var version int
	err := db.QueryRowContext(ctx, `SELECT version FROM pasture_default_llm_initialization WHERE singleton_id=0`).Scan(&version)
	if err == nil {
		if version != defaultLLMInitializationVersion {
			return defaultLLMInitializationError(fmt.Errorf("unsupported marker version %d", version))
		}
		return ctx.Err()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return defaultLLMInitializationError(err)
	}
	if hook {
		return defaultLLMInitializationError(errors.New("the default actor has not been initialized; run a normal pasture command before retrying the hook"))
	}
	registrar, ok := tr.(provenance.FixedSoftwareAgentRegistrarContext)
	if !ok {
		return defaultLLMInitializationError(errors.New("the task store lacks context-aware fixed software registration; upgrade to a compatible provenance build"))
	}
	reg := provadapter.DefaultLLMActorRegistration()
	agent, err := registrar.RegisterFixedSoftwareAgentContext(ctx, reg)
	if err != nil {
		return defaultLLMInitializationError(err)
	}
	if agent.ID != reg.Entry.ActorID || agent.Kind != reg.Entry.ActorKind || agent.Name != reg.AgentName || agent.Version != reg.Version || agent.Source != reg.Source {
		return defaultLLMInitializationError(errors.New("fixed registration returned an identity that differs from the requested descriptor"))
	}
	if err := ctx.Err(); err != nil {
		return defaultLLMInitializationError(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO pasture_default_llm_initialization(singleton_id, version) VALUES (0, ?)`, defaultLLMInitializationVersion); err != nil {
		return defaultLLMInitializationError(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT version FROM pasture_default_llm_initialization WHERE singleton_id=0`).Scan(&version); err != nil {
		return defaultLLMInitializationError(fmt.Errorf("initialization marker could not be read back: %w", err))
	}
	if version != defaultLLMInitializationVersion {
		return defaultLLMInitializationError(fmt.Errorf("initialization marker has unsupported version %d", version))
	}
	if err := ctx.Err(); err != nil {
		return defaultLLMInitializationError(err)
	}
	return nil
}

func defaultLLMInitializationError(err error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryStorage,
		What: "Pasture could not initialize or verify its default LLM actor.",
		Why:  err.Error(), Where: "Opening the unified store (internal/tasks/default_llm_init.go).",
		Impact: "No tracker was returned. A successfully committed actor may remain without a completed marker.",
		Fix:    "Inspect the fixed actor and storage error; repair conflicts through a reviewed migration, then run a normal pasture command to retry.", Cause: err}
}
