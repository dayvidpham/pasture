package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// EpochController is the substrate-neutral control surface the epoch lifecycle
// and signal handlers depend on. Keeping handlers behind this narrow interface
// (instead of the durable engine directly) keeps them unit-testable with a fake
// and isolates them from the substrate. The production implementation is
// DBOS-backed; lifecycle verbs map to RunWorkflow/CancelWorkflow and signals map
// to Send by topic.
type EpochController interface {
	// StartEpoch launches the durable control workflow for epochId (its
	// workflow id is the epoch id, so signals address it by that id).
	StartEpoch(ctx context.Context, epochId string) error
	// CancelEpoch requests cancellation of the running epoch workflow. Both the
	// cancel and terminate verbs map here (the substrate has one stop path).
	CancelEpoch(ctx context.Context, epochId string) error
	// AdvancePhase delivers an advance-phase signal.
	AdvancePhase(ctx context.Context, epochId string, sig protocol.PhaseAdvanceSignal) error
	// SubmitVote delivers a review-vote signal.
	SubmitVote(ctx context.Context, epochId string, sig protocol.ReviewVoteSignal) error
	// ReportSliceProgress delivers a slice-progress signal.
	ReportSliceProgress(ctx context.Context, epochId string, sig protocol.SliceProgressSignal) error
	// RegisterSession delivers a register-session signal.
	RegisterSession(ctx context.Context, epochId string, sig protocol.RegisterSessionSignal) error
	// StartSlice delivers a start-slice configuration signal to a slice
	// sub-workflow (addressed by its slice workflow id).
	StartSlice(ctx context.Context, sliceId string, sig protocol.SliceStartSignal) error
	// CompleteSlice delivers a complete-slice override signal to a slice
	// sub-workflow (addressed by its slice workflow id).
	CompleteSlice(ctx context.Context, sliceId string, sig protocol.SliceCompleteSignal) error
	// Close releases the controller's resources.
	Close() error
}

// dbosController is the DBOS-backed EpochController. It owns an engine and maps
// each operation onto the durable substrate.
type dbosController struct {
	e *engine.Engine
}

// OpenEpochController opens a DBOS-backed controller on the unified database.
// Empty dbPath resolves to tasks.DefaultDBPath(). The returned controller owns
// an engine launched with the pinned executor id and application version, so a
// later daemon recovers any epoch this controller starts; Close releases it.
//
// The durable epoch runs on the substrate; a single-shot CLI process starts or
// signals it and exits. The long-running daemon that hosts and recovers the
// workflow across process lifetimes is wired when the legacy workflow server is
// removed (see https://github.com/dayvidpham/pasture/issues/13).
func OpenEpochController(dbPath string) (EpochController, error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ExecutorID:         engine.DefaultExecutorID,
		ApplicationVersion: engine.DefaultApplicationVersion,
	})
	if err != nil {
		return nil, err
	}
	if err := e.Launch(); err != nil {
		e.Shutdown(5 * time.Second)
		return nil, err
	}
	return &dbosController{e: e}, nil
}

func (c *dbosController) StartEpoch(ctx context.Context, epochId string) error {
	_, err := dbos.RunWorkflow(c.e.DBOS(), c.e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId))
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("The epoch %q couldn't be started.", epochId),
			Why:      "The durable engine rejected the start request — likely a storage or engine initialisation failure.",
			Where:    "Starting the epoch (internal/handlers/controller.go in dbosController.StartEpoch).",
			Impact:   "The epoch did not start, so no phase transitions will run for it.",
			Fix: "1. Confirm the database is readable and writable:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Retry once you've confirmed the database is healthy.\n" +
				"   Note: starting an epoch with an id that is already running is an idempotent no-op.",
			Cause: err,
		}
	}
	return nil
}

func (c *dbosController) CancelEpoch(ctx context.Context, epochId string) error {
	if err := dbos.CancelWorkflow(c.e.DBOS(), epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't stop the epoch %q.", epochId),
			Why:      "The durable engine rejected the cancellation (the epoch may not be running).",
			Where:    "Cancelling the epoch (internal/handlers/controller.go in dbosController.CancelEpoch).",
			Impact:   "The epoch is unchanged; the cancellation did not take effect.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

// sendSignal is the shared send path; topic names come from the typed constants.
func (c *dbosController) sendSignal(epochId string, topic protocol.SignalTopic, send func() error) error {
	if err := send(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't deliver the %q signal to epoch %q.", topic, epochId),
			Why:      "The durable engine rejected the signal (the epoch may not be running).",
			Where:    "Delivering an epoch signal (internal/handlers/controller.go in dbosController.sendSignal).",
			Impact:   "The signal was not delivered, so the epoch's state is unchanged.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

func (c *dbosController) AdvancePhase(ctx context.Context, epochId string, sig protocol.PhaseAdvanceSignal) error {
	return c.sendSignal(epochId, protocol.SignalAdvancePhase, func() error {
		return dbos.Send(c.e.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String())
	})
}

func (c *dbosController) SubmitVote(ctx context.Context, epochId string, sig protocol.ReviewVoteSignal) error {
	return c.sendSignal(epochId, protocol.SignalSubmitVote, func() error {
		return dbos.Send(c.e.DBOS(), epochId, sig, protocol.SignalSubmitVote.String())
	})
}

func (c *dbosController) ReportSliceProgress(ctx context.Context, epochId string, sig protocol.SliceProgressSignal) error {
	return c.sendSignal(epochId, protocol.SignalSliceProgress, func() error {
		return dbos.Send(c.e.DBOS(), epochId, sig, protocol.SignalSliceProgress.String())
	})
}

func (c *dbosController) RegisterSession(ctx context.Context, epochId string, sig protocol.RegisterSessionSignal) error {
	return c.sendSignal(epochId, protocol.SignalRegisterSession, func() error {
		return dbos.Send(c.e.DBOS(), epochId, sig, protocol.SignalRegisterSession.String())
	})
}

func (c *dbosController) StartSlice(ctx context.Context, sliceId string, sig protocol.SliceStartSignal) error {
	return c.sendSignal(sliceId, protocol.SignalStartSlice, func() error {
		return dbos.Send(c.e.DBOS(), sliceId, sig, protocol.SignalStartSlice.String())
	})
}

func (c *dbosController) CompleteSlice(ctx context.Context, sliceId string, sig protocol.SliceCompleteSignal) error {
	return c.sendSignal(sliceId, protocol.SignalCompleteSlice, func() error {
		return dbos.Send(c.e.DBOS(), sliceId, sig, protocol.SignalCompleteSlice.String())
	})
}

func (c *dbosController) Close() error {
	c.e.Shutdown(5 * time.Second)
	return nil
}
