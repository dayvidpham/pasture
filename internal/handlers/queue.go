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

// queueFacts is what the commands need to know about one queue: the name it is
// stored under, and whether its concurrency is an operator's to change.
type queueFacts struct {
	storedName string
	// adjustable is false for a queue whose concurrency is fixed by design. The
	// commands refuse to change such a queue rather than accepting the change
	// and having it ignored.
	adjustable bool
}

// queueSelectorFacts is the one table of what pasture's queues are. The stored
// names come from the engine, so each name has a single definition in the
// codebase.
//
// The control queue is NOT adjustable. It carries the driver of one epoch, and
// that driver runs for as long as the epoch does, so one job at a time means one
// epoch at a time in each process — which is the intent. The full reasoning is
// where the number is set: newControlQueue in internal/engine/queue.go. Nothing
// in pasture offers a different value, so accepting a change here would write a
// number that the next daemon start replaces with one, and the operator would
// watch their change disappear. The commands refuse it instead.
var queueSelectorFacts = map[QueueSelector]queueFacts{
	QueueSelectorSlice:   {storedName: engine.SliceQueueName, adjustable: true},
	QueueSelectorControl: {storedName: engine.ControlQueueName, adjustable: false},
}

// StoredName returns the queue name this selector addresses in the database,
// and whether the selector is one pasture knows.
func (s QueueSelector) StoredName() (string, bool) {
	facts, ok := queueSelectorFacts[s]
	return facts.storedName, ok
}

// Adjustable reports whether an operator may change this queue's concurrency.
func (s QueueSelector) Adjustable() bool {
	return queueSelectorFacts[s].adjustable
}

// QueueSelectors returns every selector an operator may name, in a stable
// order, for help text and error messages.
func QueueSelectors() []QueueSelector {
	out := make([]QueueSelector, 0, len(queueSelectorFacts))
	for s := range queueSelectorFacts {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// fixedConcurrencyError refuses a change to a queue whose concurrency is fixed
// by design. It is a refusal, not a failure: nothing is wrong with the
// database, and there is nothing for the operator to repair.
func fixedConcurrencyError(selector QueueSelector, storedName string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("The %s queue does not have a concurrency setting to change.", selector),
		Why: fmt.Sprintf("%s carries the driver of one epoch, and that driver runs for as long as the epoch does "+
			"rather than for the length of one step. One job at a time therefore means one epoch at a time in each "+
			"process, which is deliberate: a second epoch waits in the queue instead of competing with the first for "+
			"the same workers and the same database. The number is fixed where the queue is created, and no option or "+
			"environment variable changes it.", storedName),
		Where:  "Changing a queue's concurrency (internal/handlers/queue.go in handlers.SetQueueConcurrency).",
		Impact: "Nothing was changed. A number written here would be replaced by one the next time the daemon starts.",
		Fix: fmt.Sprintf("1. Read the setting instead:\n"+
			"     pasture queue concurrency get %s\n"+
			"2. To change how much work runs at once, change the queue that carries it:\n"+
			"     pasture queue concurrency set %s <jobs>", selector, QueueSelectorSlice),
	}
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
// text, one per line, short name first, and says which of them can be changed.
func queueChoiceList() string {
	var b strings.Builder
	for _, s := range QueueSelectors() {
		stored, _ := s.StoredName()
		note := "read only, its concurrency is fixed"
		if s.Adjustable() {
			note = "its concurrency can be changed"
		}
		fmt.Fprintf(&b, "  %s (stored as %s; %s)\n", s, stored, note)
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

	return withQueueClient(in.DBPath, func(client dbos.Client) (int, error) {
		queue, err := retrieveQueue(client, storedName)
		if err != nil {
			return pasterrors.ExitCode(err), err
		}
		return printQueueConcurrency(storedName, queue.GetWorkerConcurrency(), format)
	})
}

// SetQueueConcurrency changes the limit on concurrent jobs for one queue, then
// reads the stored row back and prints the limit that is actually in force.
//
// The read-back is part of the operation, not a courtesy: the write and the
// read are separate database round-trips, and the row is shared, so another
// process can change it in between. The operator is told what the database
// holds, never merely what they asked for.
func SetQueueConcurrency(in QueueConcurrencyInput, format types.OutputFormat) (int, error) {
	selector, storedName, err := ResolveQueueSelector(in.Queue)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if !selector.Adjustable() {
		e := fixedConcurrencyError(selector, storedName)
		return pasterrors.ExitCode(e), e
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

	return withQueueClient(in.DBPath, func(client dbos.Client) (int, error) {
		return setStoredConcurrency(client, storedName, in.Limit, format)
	})
}

// setStoredConcurrency writes one queue's limit and reports the limit that is in
// force afterwards.
func setStoredConcurrency(client dbos.Client, storedName string, wanted int, format types.OutputFormat) (int, error) {
	queue, err := retrieveQueue(client, storedName)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	limit := wanted
	if err := queue.SetWorkerConcurrency(client, &limit); err != nil {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't set %s to %d concurrent jobs per process.", storedName, wanted),
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
	if inForce == nil || *inForce != wanted {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf("Asked for %d concurrent jobs on %s, but the stored setting now says %s.",
				wanted, storedName, describeConcurrency(inForce)),
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

// withQueueClient runs one queue operation against a database-backed client and
// releases the client afterwards.
//
// The client comes from openClient, the same constructor the epoch controller
// uses, so the queue commands and the daemon share one process identity and
// therefore act on the same rows.
//
// A release that does not finish is reported when the operation itself
// succeeded, because it means the runtime lost its database handle mid-flight
// and the operator should not trust what they just read. When the operation
// already failed, its own error is the one worth reporting.
//
// The release runs from a deferred call, so it happens on EVERY way out of the
// operation — including a panic inside it, and including any early return a
// later change adds. Releasing on the ordinary path only would leave a durable
// client, and the database handle it owns, held by a process that is on its way
// out.
func withQueueClient(dbPath string, fn func(dbos.Client) (int, error)) (code int, err error) {
	client, _, release, openErr := openClient(dbPath, releaseSiteQueueCommand)
	if openErr != nil {
		return pasterrors.ExitCode(openErr), openErr
	}
	defer func() {
		code, err = queueCommandResult(code, err, release())
	}()
	return fn(client)
}

// queueCommandResult decides what a queue command reports when the operation and
// the release of the client can each fail.
//
// The operation's own failure wins: it is what the operator asked about, and it
// is the more useful of the two. A release that did not finish is reported when
// the operation succeeded, and then it MUST be reported: it means the durable
// runtime closed its database handle while part of it was still running, so what
// the command just printed may not be the last word on that row. Reporting it
// costs the operator one confusing line; swallowing it costs them a wrong belief.
func queueCommandResult(code int, opErr, releaseErr error) (int, error) {
	if opErr != nil {
		return code, opErr
	}
	if releaseErr != nil {
		return pasterrors.ExitCode(releaseErr), releaseErr
	}
	return code, nil
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
