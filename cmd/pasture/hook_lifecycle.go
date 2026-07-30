package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
)

type lifecycleCLIClock struct{}

func (lifecycleCLIClock) Now() time.Time { return time.Now() }

type lifecycleCLIOperations struct{}

func (lifecycleCLIOperations) NewOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create lifecycle operation identity from the operating system random source: %w", err)
	}
	return "pasture.lifecycle." + hex.EncodeToString(value[:]), nil
}

var hookLifecycleCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Record a native harness lifecycle event",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		harness, _ := cmd.Flags().GetString("harness")
		event, _ := cmd.Flags().GetString("event")
		hostVersion, _ := cmd.Flags().GetString("host-version")
		err := handlers.HookLifecycle(cmd.Context(), handlers.HookLifecycleInput{
			DBPath: flagDBPath, Harness: ir.HarnessID(harness), Event: event,
			HostVersion: hostVersion, Input: cmd.InOrStdin(), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
		})
		if err == nil {
			return nil
		}
		printError(err)
		exitWithCode(pasterrors.ExitCode(err))
		return nil
	},
}

func init() {
	flags := hookLifecycleCmd.Flags()
	flags.String("harness", "", "Native harness whose payload is on standard input (required)")
	flags.String("event", "", "Native event this generated hook is registered for (required)")
	flags.String("host-version", "", "Observed native host version to retain with this occurrence (required)")
	for _, name := range []string{"harness", "event", "host-version"} {
		if err := hookLifecycleCmd.MarkFlagRequired(name); err != nil {
			panic(fmt.Sprintf("pasture: mark lifecycle flag %q required: %v", name, err))
		}
	}
	hookCmd.AddCommand(hookLifecycleCmd)
}
