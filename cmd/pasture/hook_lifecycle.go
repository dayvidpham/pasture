package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
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
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "pasture: lifecycle hook recovered from panic %v; the event may not have been recorded; inspect the database and retry the hook input\n", recovered)
				runErr = nil
			}
		}()
		if len(args) != 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "pasture: lifecycle hook received unexpected positional arguments %q; pass hook coordinates through flags\n", args)
			return nil
		}
		harness, _ := cmd.Flags().GetString("harness")
		event, _ := cmd.Flags().GetString("event")
		hostVersion, _ := cmd.Flags().GetString("host-version")
		response, err := handlers.HookLifecycleResponse(cmd.Context(), handlers.HookLifecycleInput{
			DBPath: flagDBPath, Harness: ir.HarnessID(harness), Event: event,
			HostVersion: hostVersion, Input: cmd.InOrStdin(), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
		})
		if err == nil {
			if response.IsValid() {
				if encodeErr := writeLifecycleResponse(cmd.OutOrStdout(), response); encodeErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "pasture: lifecycle hook could not encode its committed host response: %v; the event was recorded but the host received no decision; inspect the database and retry the hook input\n", encodeErr)
					return nil
				}
			}
			return nil
		}
		printError(err)
		return nil
	},
}

func writeLifecycleResponse(out io.Writer, response json.Marshaler) error {
	encoded, err := response.MarshalJSON()
	if err != nil {
		return err
	}
	_, err = out.Write(encoded)
	return err
}

func init() {
	flags := hookLifecycleCmd.Flags()
	flags.String("harness", "", "Native harness whose payload is on standard input (required)")
	flags.String("event", "", "Native event this generated hook is registered for (required)")
	flags.String("host-version", "", "Observed native host version to retain with this occurrence (required)")
	hookLifecycleCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if cmd != hookLifecycleCmd {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "pasture: lifecycle hook flag error: %v; inspect the generated hook command and retry\n", err)
		return nil
	})
	hookCmd.AddCommand(hookLifecycleCmd)
}
