package handlers

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
)

type HookLifecycleListInput struct {
	DBPath                      string
	Contracts, Events, Bindings []string
	PageSize                    uint16
	Cursor                      *model.Cursor
}

func HookLifecycleList(ctx context.Context, out io.Writer, in HookLifecycleListInput, format string) error {
	if ctx == nil || out == nil || in.PageSize == 0 || int(in.PageSize) > model.MaxPageSize || (format != "text" && format != "json") {
		return fmt.Errorf("list lifecycle records: context, output, page size 1..%d, and format text|json are required", model.MaxPageSize)
	}
	query := model.OccurrenceQuery{Page: model.PageRequest{Size: model.PageSize(in.PageSize), Cursor: in.Cursor}}
	for _, raw := range in.Contracts {
		var id ir.RuntimeContractID
		if err := id.UnmarshalJSON([]byte(strconv.Quote(raw))); err != nil {
			return fmt.Errorf("list lifecycle records: invalid contract %q: %w", raw, err)
		}
		query.Contracts = append(query.Contracts, id)
	}
	for _, raw := range in.Events {
		value, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || value == 0 {
			return fmt.Errorf("list lifecycle records: event %q must be a positive typed ordinal", raw)
		}
		query.Events = append(query.Events, model.ContractEventKind(value))
	}
	for _, raw := range in.Bindings {
		binding, err := parseLifecycleBinding(raw)
		if err != nil {
			return err
		}
		query.Bindings = append(query.Bindings, binding)
	}
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return err
	}
	defer tracker.Close()
	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
		return err
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		return err
	}
	page, err := reader.Records(ctx, query)
	if err != nil {
		return err
	}
	return formatters.HookLifecycle(out, page, format)
}

func parseLifecycleBinding(raw string) (model.NativeBinding, error) {
	left, value, ok := strings.Cut(raw, "=")
	if !ok {
		return model.NativeBinding{}, fmt.Errorf("list lifecycle records: binding %q must use <kind>:<native-name>=<exact-value>", raw)
	}
	kind, name, ok := strings.Cut(left, ":")
	if !ok || name == "" || value == "" {
		return model.NativeBinding{}, fmt.Errorf("list lifecycle records: binding %q is incomplete", raw)
	}
	kinds := map[string]model.NativeBindingKind{"session": model.BindingSession, "turn": model.BindingTurn, "request": model.BindingRequest, "tool-call": model.BindingToolCall, "agent": model.BindingAgent, "message": model.BindingMessage, "task": model.BindingTask, "worktree": model.BindingWorktree}
	typed, ok := kinds[kind]
	if !ok {
		return model.NativeBinding{}, fmt.Errorf("list lifecycle records: binding kind %q is unknown", kind)
	}
	return model.NativeBinding{Kind: typed, NativeName: name, Value: value}, nil
}
