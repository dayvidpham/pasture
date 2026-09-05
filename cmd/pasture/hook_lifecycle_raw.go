package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

var rawHookHarness string
var rawHookEvent string
var rawHookHostVersion string
var rawHookSchemaVersion string
var rawHookDryRun bool

// hookLifecycleRawCmd is the raw-ingestion escape hatch (URD R4.1). The extra
// path segment "raw" IS the visible mark: raw ingestion is for imports and
// migration, never the default path (authority §10). The cobra Long text is
// SLICE-4-owned (cmd/pasture/hook_lifecycle_raw_help.go) and is rendered
// verbatim so the non-recommended posture leads the help output.
var hookLifecycleRawCmd = &cobra.Command{
	Use:   hookLifecycleRawUse,
	Short: "Ingest a raw lifecycle event (imports and migration; not the default path)",
	Long:  hookLifecycleRawLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "pasture: lifecycle raw hook recovered from panic %v; the event may not have been recorded; inspect the database and retry the hook input\n", recovered)
				runErr = nil
			}
		}()
		// THE RAW PATH RUNS UNDER A DEADLINE, the WorkflowResult tier, the
		// longest window any writer of the store may hold. It exists so that
		// the time between a payload blob's write and its journal append is
		// BOUNDED on every writer: the native hook bounds its own work at the
		// hook-invocation tier, and without this deadline an import could sit
		// between its two durable writes for as long as a lock lasted, which
		// is what makes an age bound on orphan blobs a false claim. A raw
		// invocation that reaches the tier reports the expiry as a fault
		// below and records nothing further; the payload blob it may have
		// written is a reclaimable orphan, never a journal row naming an
		// absent blob.
		ctx, cancel := context.WithTimeout(cmd.Context(), timeouts.ProductionProfile().WorkflowResult())
		defer cancel()
		// HookLifecycleRaw mirrors the native commit-before-stdout surface:
		// the canonical Proceed bytes are emitted only on the nil-error path,
		// so nothing reaches stdout before the durable commit completes.
		ack, err := handlers.HookLifecycleRaw(ctx, handlers.HookLifecycleRawInput{
			DBPath:        flagDBPath,
			Harness:       ir.HarnessID(rawHookHarness),
			Event:         rawHookEvent,
			HostVersion:   rawHookHostVersion,
			SchemaVersion: handlers.RawSchemaVersion(rawHookSchemaVersion),
			DryRun:        rawHookDryRun,
			Input:         cmd.InOrStdin(),
			Clock:         lifecycleCLIClock{},
			Operations:    lifecycleCLIOperations{},
		})
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fmt.Fprintln(cmd.ErrOrStderr(), rawDeadlineDiagnostic(timeouts.ProductionProfile().WorkflowResult(), rawHookEvent, rawHookHarness))
			}
			printError(err)
			return nil
		}
		if len(ack) > 0 {
			if _, writeErr := cmd.OutOrStdout().Write(ack); writeErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "pasture: lifecycle raw hook could not write its committed host continuation: %v; the event was recorded but the host received no continuation; inspect the database and retry the hook input\n", writeErr)
				return nil
			}
		}
		return nil
	},
}

// rawDeadlineDiagnostic is the sentence a raw import reads when it reaches the
// import deadline. It names the tier so the reader knows the bound is the
// store's longest writer window and not a network or host budget, and it
// says what state the store is in: no occurrence, at most one reclaimable
// payload blob. The error that follows it is the refusal the store returned.
func rawDeadlineDiagnostic(tier interface{ String() string }, event, harness string) string {
	return fmt.Sprintf("pasture: lifecycle raw hook stopped at its %s import deadline while ingesting event %q of harness %q; "+
		"this happened in hookLifecycleRawCmd (cmd/pasture/hook_lifecycle_raw.go); no occurrence was committed for this payload, "+
		"and the payload blob it may have written is a reclaimable orphan; the usual reason is another writer holding the "+
		"pasture store, so find that writer or retry once it releases the store", tier, event, harness)
}

func init() {
	f := hookLifecycleRawCmd.Flags()
	f.StringVar(&rawHookHarness, "harness", "", "Native harness whose payload is on standard input (required)")
	f.StringVar(&rawHookEvent, "event", "", "Native event this generated hook is registered for (required)")
	f.StringVar(&rawHookHostVersion, "host-version", "", "Observed native host version to retain with this occurrence (required)")
	f.StringVar(&rawHookSchemaVersion, "schema-version", "", "Wire-level schema identity this payload conforms to; must name a version pinned in this build (required)")
	f.BoolVar(&rawHookDryRun, "dry-run", false, "Preview what would be committed (same verification, no database opened or written)")
	hookLifecycleCmd.AddCommand(hookLifecycleRawCmd)
}
