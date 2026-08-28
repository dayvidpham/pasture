package handlers

// queue.go is the operator surface for the work queues that pasture dispatches
// slice, review and epoch-control work through.
//
// A queue's settings live in a row of the pasture database, shared by every
// pasture process, and a running daemon reloads that row as it polls. So this
// command does not need to reach the daemon, and there is no reload signal to
// send: it writes the row, and the daemon adopts the change on its next poll,
// about a second later. Work already running is not interrupted; the new limit
// governs the next job the daemon picks up.
//
// A change made here lasts until the daemon starts again: a starting daemon
// writes the limit it is configured with into the same row. That is deliberate
// — a process that is about to serve a queue governs it — but it means this
// command is for adjusting a daemon that is already running, not for setting a
// lasting default. The command's help text tells the operator so, and
// TestSetQueueConcurrency_IsReplacedWhenTheDaemonStartsAgain pins it.
//
// The same row is written in-process by engine.Engine.SetSliceConcurrency,
// which is the path a process that hosts the engine uses. This file is the path
// for an operator standing outside that process. The two are equivalent, and
// whichever writes last wins.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
)

// QueueSelector is the short, operator-facing name of one pasture work queue.
// The stored name is longer and is what appears in the database and in logs;
// both forms are accepted on the command line.
type QueueSelector string

const (
	// QueueSelectorSlice addresses the queue that slice and review work runs on.
	QueueSelectorSlice QueueSelector = "slice"
	// QueueSelectorControl addresses the queue that epoch control work runs on.
	QueueSelectorControl QueueSelector = "control"
)

// queueSelectorNames maps each selector to the queue name stored in the
// database. The stored names come from the engine, so there is one definition
// of each name in the codebase.
var queueSelectorNames = map[QueueSelector]string{
	QueueSelectorSlice:   engine.SliceQueueName,
	QueueSelectorControl: engine.ControlQueueName,
}

// StoredName returns the queue name this selector addresses in the database,
// and whether the selector is one pasture knows.
func (s QueueSelector) StoredName() (string, bool) {
	name, ok := queueSelectorNames[s]
	return name, ok
}

// QueueSelectors returns every selector an operator may name, in a stable
// order, for help text and error messages.
func QueueSelectors() []QueueSelector {
	out := make([]QueueSelector, 0, len(queueSelectorNames))
	for s := range queueSelectorNames {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ResolveQueueSelector maps one command-line argument to a known queue. Both
// the short selector ("slice") and the stored name ("pasture-slice-queue") are
// accepted, because the stored name is what an operator sees in the database
// and in log lines and is therefore what they will paste in.
func ResolveQueueSelector(arg string) (QueueSelector, string, error) {
	trimmed := strings.TrimSpace(arg)
	for _, s := range QueueSelectors() {
		stored, _ := s.StoredName()
		if trimmed == string(s) || trimmed == stored {
			return s, stored, nil
		}
	}
	return "", "", &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("%q is not a pasture work queue.", arg),
		Why:      "The queue name did not match any queue this version of pasture manages.",
		Where:    "Resolving the queue name (internal/handlers/queue.go in handlers.ResolveQueueSelector).",
		Impact:   "No queue setting was read or changed.",
		Fix:      "Name one of these queues:\n" + queueChoiceList(),
	}
}

// queueChoiceList renders the accepted queue names for an error message or help
// text, one per line, short name first.
func queueChoiceList() string {
	var b strings.Builder
	for _, s := range QueueSelectors() {
		stored, _ := s.StoredName()
		fmt.Fprintf(&b, "  %s (stored as %s)\n", s, stored)
	}
	return strings.TrimRight(b.String(), "\n")
}

// QueueConcurrencyInput names the queue to act on, and where the database is.
type QueueConcurrencyInput struct {
	// DBPath is the unified pasture database. Empty resolves to the default.
	DBPath string
	// Queue is the operator's argument: a short selector or a stored name.
	Queue string
	// Limit is the new number of concurrent jobs per process. It is read by
	// SetQueueConcurrency only, and must be positive.
	Limit int
}

// QueueConcurrency prints the stored limit on concurrent jobs for one queue.
// It returns the process exit code and the error to report, following the same
// shape as the other command handlers.
func QueueConcurrency(in QueueConcurrencyInput, format types.OutputFormat) (int, error) {
	_, storedName, err := ResolveQueueSelector(in.Queue)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	client, closeClient, err := openQueueClient(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer closeClient()

	queue, err := retrieveQueue(client, storedName)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	return printQueueConcurrency(storedName, queue.GetWorkerConcurrency(), format)
}

// SetQueueConcurrency changes the limit on concurrent jobs for one queue, then
// reads the stored row back and prints the limit that is actually in force.
//
// The read-back is part of the operation, not a courtesy: the write and the
// read are separate database round-trips, and the row is shared, so another
// process can change it in between. The operator is told what the database
// holds, never merely what they asked for.
func SetQueueConcurrency(in QueueConcurrencyInput, format types.OutputFormat) (int, error) {
	_, storedName, err := ResolveQueueSelector(in.Queue)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if in.Limit <= 0 {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%d is not a usable number of concurrent jobs.", in.Limit),
			Why:      "The number must be a positive whole number; zero or less would stop the queue from running any work.",
			Where:    "Changing a queue's concurrency (internal/handlers/queue.go in handlers.SetQueueConcurrency).",
			Impact:   "Nothing was changed; the queue keeps the setting it had.",
			Fix:      fmt.Sprintf("Give a positive whole number, for example the default %d for slice work.", engine.DefaultSliceQueueConcurrency),
		}
		return pasterrors.ExitCode(e), e
	}

	client, closeClient, err := openQueueClient(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer closeClient()

	queue, err := retrieveQueue(client, storedName)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	limit := in.Limit
	if err := queue.SetWorkerConcurrency(client, &limit); err != nil {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't set %s to %d concurrent jobs per process.", storedName, in.Limit),
			Why: "The setting is stored in the pasture database and the database refused the change — " +
				"usually because the file is unwritable, or another process is holding it.",
			Where:  "Changing a queue's concurrency (internal/handlers/queue.go in handlers.SetQueueConcurrency).",
			Impact: "The queue keeps the setting it had, and any running daemon keeps working at that setting.",
			Fix: "1. Confirm the database file is present and writable:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Confirm no other pasture process is holding it:\n" +
				"     pgrep -fa 'pasture|pastured'\n" +
				"3. Retry once the database is healthy.",
			Cause: err,
		}
		return pasterrors.ExitCode(e), e
	}

	persisted, err := retrieveQueue(client, storedName)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	inForce := persisted.GetWorkerConcurrency()
	if inForce == nil || *inForce != in.Limit {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf("Asked for %d concurrent jobs on %s, but the stored setting now says %s.",
				in.Limit, storedName, describeConcurrency(inForce)),
			Why: "Queue settings are shared by every pasture process, and another process changed the same " +
				"setting between this change and the read that confirmed it.",
			Where:  "Reading a queue's concurrency back (internal/handlers/queue.go in handlers.SetQueueConcurrency).",
			Impact: "The stored setting, not the one asked for, is what the queue now runs work at.",
			Fix: "1. Find the other pasture process that is changing the setting:\n" +
				"     pgrep -fa 'pasture|pastured'\n" +
				"2. Decide which setting is wanted, then set it once, from one place only.",
		}
		return pasterrors.ExitCode(e), e
	}
	return printQueueConcurrency(storedName, inForce, format)
}

// describeConcurrency renders a stored limit for an error message.
func describeConcurrency(limit *int) string {
	if limit == nil {
		return "no limit at all"
	}
	return fmt.Sprintf("%d", *limit)
}

// printQueueConcurrency renders one queue's setting to stdout.
func printQueueConcurrency(storedName string, limit *int, format types.OutputFormat) (int, error) {
	out, err := formatters.FormatQueueConcurrency(formatters.QueueConcurrency{
		Queue:             storedName,
		WorkerConcurrency: limit,
	}, format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Println(out)
	return 0, nil
}

// openQueueClient opens a database-backed client on the shared pasture
// database, reusing the controller the lifecycle verbs already open so there is
// one place in the codebase that builds a client. The returned function
// releases it.
func openQueueClient(dbPath string) (dbos.Client, func(), error) {
	controller, err := OpenEpochController(dbPath)
	if err != nil {
		return nil, nil, err
	}
	backed, ok := controller.(*dbosController)
	if !ok || backed.client == nil {
		_ = controller.Close()
		return nil, nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     "Couldn't get a database client to read the work-queue settings.",
			Why:      "The opened controller is not the database-backed one, so it carries no client.",
			Where:    "Opening a queue client (internal/handlers/queue.go in handlers.openQueueClient).",
			Impact:   "No queue setting can be read or changed.",
			Fix:      "Retry against the unified pasture database, passing --db or setting PASTURE_DB_PATH if it is not in the default location.",
		}
	}
	return backed.client, func() { _ = controller.Close() }, nil
}

// retrieveQueue reads one queue's stored settings and turns the two failures an
// operator can actually meet into actionable errors: the queue has never been
// registered (no daemon has run against this database), and the database
// refused the read.
func retrieveQueue(client dbos.Client, storedName string) (dbos.Queue, error) {
	queue, err := dbos.RetrieveQueue(client, storedName)
	if err == nil {
		return queue, nil
	}
	if errors.Is(err, dbos.ErrQueueNotFound) {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The work queue %s is not in this pasture database.", storedName),
			Why:      "A queue's settings are written by the daemon when it starts, and no daemon has started against this database yet.",
			Where:    "Reading a queue's settings (internal/handlers/queue.go in handlers.retrieveQueue).",
			Impact:   "There is no setting to read or change until the daemon has run once.",
			Fix: "1. Start the daemon so it registers its queues:\n" +
				"     pastured\n" +
				"2. Then run this command again.\n" +
				"3. If the daemon uses another database, pass the same --db or PASTURE_DB_PATH here.",
			Cause: err,
		}
	}
	return nil, &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("Couldn't read the settings of the work queue %s.", storedName),
		Why:      "The pasture database refused the read — usually because the file is unreadable or is held by another process.",
		Where:    "Reading a queue's settings (internal/handlers/queue.go in handlers.retrieveQueue).",
		Impact:   "The queue's setting can't be shown, and it was not changed.",
		Fix: "1. Confirm the database file is present and readable:\n" +
			"     ls -l ~/.local/share/pasture/pasture.db\n" +
			"2. Confirm no other pasture process is holding it:\n" +
			"     pgrep -fa 'pasture|pastured'\n" +
			"3. Retry once the database is healthy.",
		Cause: err,
	}
}
