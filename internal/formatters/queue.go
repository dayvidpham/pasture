package formatters

// queue.go renders the work-queue settings an operator can read and change.

import (
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/types"
)

// QueueConcurrency is one queue's per-process limit on concurrent jobs, as it
// is stored in the pasture database.
//
// WorkerConcurrency is a pointer because "no limit" and "a limit of zero" are
// different states in storage and must not be rendered the same way. nil means
// the queue runs as many jobs at once as it can dequeue.
type QueueConcurrency struct {
	Queue             string
	WorkerConcurrency *int
}

type queueConcurrencyJSON struct {
	Queue             string `json:"queue"`
	WorkerConcurrency *int   `json:"worker_concurrency"`
}

// FormatQueueConcurrency renders one queue's concurrency setting.
func FormatQueueConcurrency(q QueueConcurrency, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(queueConcurrencyJSON{
			Queue:             q.Queue,
			WorkerConcurrency: q.WorkerConcurrency,
		}, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatQueueConcurrency: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the queue setting cannot be rendered as JSON",
				Fix:      "this should not happen with the typed QueueConcurrency shape; file a bug if it does",
			}
		}
		return string(b), nil
	case types.OutputText:
		if q.WorkerConcurrency == nil {
			return fmt.Sprintf("%s: no limit on concurrent jobs per process", q.Queue), nil
		}
		return fmt.Sprintf("%s: %d concurrent jobs per process", q.Queue, *q.WorkerConcurrency), nil
	default:
		return "", unknownFormatErr("FormatQueueConcurrency", format)
	}
}
