// Package handlers provides standalone handler functions for each pasture-msg
// subcommand. Handlers are independent of Cobra — they receive parsed arguments
// and return (exitCode int, err error), making them fully unit-testable.
//
// Exit code contract (D14):
//   - 0: success
//   - 1: validation or config error
//   - 2: connection error (Temporal unreachable)
//   - 3: workflow error (workflow not found, query/signal failed)
package handlers

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/converter"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// QueryState queries the full epoch state from the running EpochWorkflow.
//
// Sends a QueryFullState query to the workflow identified by epochId and
// formats the result as either JSON or human-readable text.
//
// Exit codes: 0=success, 2=connection error, 3=workflow error.
func QueryState(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochId string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	result, err := queryWorkflow[protocol.QueryStateResult](ctx, c, epochId, protocol.QueryFullState)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatEpochState(result, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// queryWorkflow executes a typed Temporal query against the given workflow.
func queryWorkflow[T any](ctx context.Context, c interface {
	QueryWorkflow(ctx context.Context, workflowId, runId, queryType string, args ...interface{}) (converter.EncodedValue, error)
}, workflowId, queryType string) (T, error) {
	var zero T
	val, err := c.QueryWorkflow(ctx, workflowId, "", queryType)
	if err != nil {
		return zero, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't read the state of epoch %q.", workflowId),
			Why:      fmt.Sprintf("The workflow server rejected the %q query.", queryType),
			Where:    "Querying the workflow state (internal/handlers/query.go in handlers.queryWorkflow).",
			Impact:   "The current workflow state can't be returned, so commands depending on it have no view to act on.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, list active epochs to find the right ID:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry the query once the epoch is healthy.",
				workflowId),
			Cause: err,
		}
	}
	var result T
	if err := val.Get(&result); err != nil {
		return zero, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("The state returned for epoch %q couldn't be decoded.", workflowId),
			Why:      "Reading the workflow query result failed — the daemon returned an unexpected shape.",
			Where:    "Querying the workflow state (internal/handlers/query.go in handlers.queryWorkflow).",
			Impact:   "The state can't be displayed because pasture-msg can't interpret what pastured sent back.",
			Fix: "1. Check the versions of pastured and pasture-msg — they must match:\n" +
				"     pastured --version\n" +
				"     pasture-msg --version\n" +
				"2. If they differ, update both to the same release.",
			Cause: err,
		}
	}
	return result, nil
}
