package main

import (
	"fmt"

	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// NewAdapterCommand constructs the private-to-module hidden adapter command.
// The CLI integration owner registers the returned command on rootCmd; this
// file intentionally performs no registration of its own.
func NewAdapterCommand() *cobra.Command {
	adapter := &cobra.Command{
		Use:                   "__adapter",
		Hidden:                true,
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     cobra.NoFileCompletions,
	}

	invoke := &cobra.Command{
		Use:                   "invoke",
		Hidden:                true,
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     cobra.NoFileCompletions,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("The hidden adapter invoke command received %d positional arguments.", len(args)),
				Why:      "The command accepts exactly one strict JSON envelope on standard input and no positional arguments.",
				Where:    "Parsing the hidden adapter command (cmd/pasture/adapter.go).",
				Impact:   "The adapter invocation was not decoded and the Pasture store was not opened.",
				Fix:      "remove all positional arguments and pipe one pasture.adapter-invocation/v1 JSON object to standard input.",
			}
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := handlers.AdapterInvoke(cmd.Context(), handlers.AdapterInvokeInput{
				DBPath: flagDBPath,
				Input:  cmd.InOrStdin(),
				Output: cmd.OutOrStdout(),
			})
			if err == nil {
				return nil
			}
			printError(err)
			exitWithCode(pasterrors.ExitCode(err))
			return nil
		},
	}

	adapter.AddCommand(invoke)
	return adapter
}
