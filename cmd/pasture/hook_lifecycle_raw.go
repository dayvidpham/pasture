package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
)

var rawHookHarness string
var rawHookEvent string
var rawHookHostVersion string
var rawHookSchemaVersion string

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
		// HookLifecycleRaw mirrors the native commit-before-stdout surface:
		// the canonical Proceed bytes are emitted only on the nil-error path,
		// so nothing reaches stdout before the durable commit completes.
		ack, err := handlers.HookLifecycleRaw(cmd.Context(), handlers.HookLifecycleRawInput{
			DBPath:        flagDBPath,
			Harness:       ir.HarnessID(rawHookHarness),
			Event:         rawHookEvent,
			HostVersion:   rawHookHostVersion,
			SchemaVersion: handlers.RawSchemaVersion(rawHookSchemaVersion),
			Input:         cmd.InOrStdin(),
			Clock:         lifecycleCLIClock{},
			Operations:    lifecycleCLIOperations{},
		})
		if err != nil {
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

func init() {
	f := hookLifecycleRawCmd.Flags()
	f.StringVar(&rawHookHarness, "harness", "", "Native harness whose payload is on standard input (required)")
	f.StringVar(&rawHookEvent, "event", "", "Native event this generated hook is registered for (required)")
	f.StringVar(&rawHookHostVersion, "host-version", "", "Observed native host version to retain with this occurrence (required)")
	f.StringVar(&rawHookSchemaVersion, "schema-version", "", "Wire-level schema identity this payload conforms to; must name a version pinned in this build (required)")
	hookLifecycleCmd.AddCommand(hookLifecycleRawCmd)
}
