package handlers

import (
	"context"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"fmt"
)

// TemporalClient is the narrow Temporal client interface used by handlers.
//
// Using a narrow interface (instead of the full client.Client) makes handlers
// trivially testable via mock injection without depending on the full SDK type.
// ExecuteWorkflow accepts client.StartWorkflowOptions directly (not interface{})
// so callers get compile-time type safety and no runtime type assertion is needed.
type TemporalClient interface {
	Close()
	QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...interface{}) (converter.EncodedValue, error)
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (TemporalWorkflowRun, error)
	CancelWorkflow(ctx context.Context, workflowID, runID string) error
	TerminateWorkflow(ctx context.Context, workflowID, runID, reason string, details ...interface{}) error
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
			What:     fmt.Sprintf("failed to connect to Temporal at %s", conn.ServerAddress),
			Why:      err.Error(),
			Impact:   "command cannot reach the Temporal server",
			Fix:      "ensure pastured is running and Temporal is reachable at the configured address",
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

func (r *realClient) QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...interface{}) (converter.EncodedValue, error) {
	return r.c.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
}

func (r *realClient) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error {
	return r.c.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (r *realClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (TemporalWorkflowRun, error) {
	run, err := r.c.ExecuteWorkflow(ctx, options, workflow, args...)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (r *realClient) CancelWorkflow(ctx context.Context, workflowID, runID string) error {
	return r.c.CancelWorkflow(ctx, workflowID, runID)
}

func (r *realClient) TerminateWorkflow(ctx context.Context, workflowID, runID, reason string, details ...interface{}) error {
	return r.c.TerminateWorkflow(ctx, workflowID, runID, reason, details...)
}
