package handlers

import (
	"context"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"fmt"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// TemporalClient is the narrow Temporal client interface used by handlers.
//
// Using a narrow interface (instead of the full client.Client) makes handlers
// trivially testable via mock injection without depending on the full SDK type.
// ExecuteWorkflow accepts client.StartWorkflowOptions directly (not interface{})
// so callers get compile-time type safety and no runtime type assertion is needed.
type TemporalClient interface {
	Close()
	QueryWorkflow(ctx context.Context, workflowId, runId, queryType string, args ...interface{}) (converter.EncodedValue, error)
	SignalWorkflow(ctx context.Context, workflowId, runId, signalName string, arg interface{}) error
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (TemporalWorkflowRun, error)
	CancelWorkflow(ctx context.Context, workflowId, runId string) error
	TerminateWorkflow(ctx context.Context, workflowId, runId, reason string, details ...interface{}) error
}

// TemporalWorkflowRun is the narrow workflow run interface used by handlers.
type TemporalWorkflowRun interface {
	GetID() string
	GetRunID() string
}

// TemporalClientFactory creates a TemporalClient from a ConnectionConfig.
// The default implementation calls client.Dial; tests inject a mock factory.
type TemporalClientFactory func(ctx context.Context, conn config.ConnectionConfig) (TemporalClient, error)

// DefaultClientFactory is the production factory that dials Temporal.
func DefaultClientFactory(ctx context.Context, conn config.ConnectionConfig) (TemporalClient, error) {
	c, err := client.Dial(client.Options{
		HostPort:  conn.ServerAddress,
		Namespace: conn.Namespace,
	})
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't connect to the workflow server at %s.", conn.ServerAddress),
			Why:      "The connection attempt was refused or timed out.",
			Where:    "Dialling the workflow server (internal/handlers/client.go in handlers.DefaultClientFactory).",
			Impact:   "No commands can be sent to running workflows until the connection is restored.",
			Fix: fmt.Sprintf("1. Check that pastured is running and listening on the right address:\n"+
				"     pastured --server-address %s\n"+
				"2. Confirm the workflow server itself is reachable:\n"+
				"     nc -vz %s\n"+
				"3. Retry the command once the connection is back.",
				conn.ServerAddress, conn.ServerAddress),
			Cause: err,
		}
	}
	return &realClient{c: c}, nil
}

// realClient wraps the full Temporal client.Client to implement TemporalClient.
// It adapts ExecuteWorkflow's return type to TemporalWorkflowRun.
type realClient struct {
	c client.Client
}

func (r *realClient) Close() { r.c.Close() }

func (r *realClient) QueryWorkflow(ctx context.Context, workflowId, runId, queryType string, args ...interface{}) (converter.EncodedValue, error) {
	return r.c.QueryWorkflow(ctx, workflowId, runId, queryType, args...)
}

func (r *realClient) SignalWorkflow(ctx context.Context, workflowId, runId, signalName string, arg interface{}) error {
	return r.c.SignalWorkflow(ctx, workflowId, runId, signalName, arg)
}

func (r *realClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (TemporalWorkflowRun, error) {
	run, err := r.c.ExecuteWorkflow(ctx, options, workflow, args...)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (r *realClient) CancelWorkflow(ctx context.Context, workflowId, runId string) error {
	return r.c.CancelWorkflow(ctx, workflowId, runId)
}

func (r *realClient) TerminateWorkflow(ctx context.Context, workflowId, runId, reason string, details ...interface{}) error {
	return r.c.TerminateWorkflow(ctx, workflowId, runId, reason, details...)
}
