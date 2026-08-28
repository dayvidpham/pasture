package main

// queue.go is the operator surface for the work queues that pasture dispatches
// slice, review and epoch-control work through.
//
// The settings live in the shared pasture database, and a running daemon
// reloads them as it polls, so these commands change a running daemon without
// restarting it and without contacting it directly.

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// queueCmd is the parent for the work-queue commands.
var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Inspect and adjust the work queues",
	Long: `Inspect and adjust the work queues that pasture dispatches work through.

A queue's settings are stored in the pasture database and are shared by every
pasture process. A running daemon re-reads them as it polls, so a change here
reaches the daemon about a second later, without restarting it. Work already
running is not interrupted; the change governs the work picked up next.`,
}

// queueConcurrencyCmd is the parent for reading and changing how much work one
// queue runs at once.
var queueConcurrencyCmd = &cobra.Command{
	Use:   "concurrency",
	Short: "Show or change how many jobs a queue runs at once",
	Long: `Show or change how many jobs one queue runs at once in a single process.

The limit is per process, not per machine: two daemons serving the same queue
each run up to this many jobs. The limit exists to bound how many jobs write to
the pasture database at the same time, because the database serialises writers.`,
}

var queueConcurrencyGetCmd = &cobra.Command{
	Use:   "get <queue>",
	Short: "Show how many jobs a queue runs at once",
	Long: `Show the stored limit on concurrent jobs for one queue.

` + queueArgumentHelp(),
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.QueueConcurrency(handlers.QueueConcurrencyInput{
			DBPath: flagDBPath,
			Queue:  args[0],
		}, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

var queueConcurrencySetCmd = &cobra.Command{
	Use:   "set <queue> <jobs>",
	Short: "Change how many jobs a queue runs at once",
	Long: `Change the limit on concurrent jobs for one queue, then show the limit that is
actually in force after the change.

The stored setting is read back after it is written, because the setting is
shared: another process can change it in the same moment. What is printed is
what the database holds, not merely what was asked for.

A running daemon adopts the new limit as it polls, about a second later. Jobs
already running are not interrupted.

Only the slice queue can be changed. The control queue runs one epoch control
workflow at a time in each process by design, and this command refuses to change
it.

The change lasts until the daemon starts again. At start-up a daemon writes the
limit it is configured with, which replaces a limit set here. For a slice limit
that survives a restart, set it where the daemon reads it: the
--slice-concurrency option or the PASTURE_SLICE_CONCURRENCY environment
variable. Those two govern the slice queue only.

` + queueArgumentHelp(),
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		jobs, convErr := strconv.Atoi(args[1])
		if convErr != nil {
			err := &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("%q is not a number of jobs.", args[1]),
				Why:      "The second argument must be a positive whole number.",
				Where:    "Reading the command arguments (cmd/pasture/queue.go).",
				Impact:   "Nothing was changed; the queue keeps the setting it had.",
				Fix:      "Give a positive whole number, for example:\n     pasture queue concurrency set slice 8",
				Cause:    convErr,
			}
			printError(err)
			exitWithCode(pasterrors.ExitCode(err))
			return nil
		}
		code, err := handlers.SetQueueConcurrency(handlers.QueueConcurrencyInput{
			DBPath: flagDBPath,
			Queue:  args[0],
			Limit:  jobs,
		}, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// queueArgumentHelp lists the queue names the commands accept, so the help text
// and the error message for a wrong name always agree. It is built from the
// same table the commands act on, so it cannot fall out of step with them.
func queueArgumentHelp() string {
	out := "Queues:\n"
	for _, s := range handlers.QueueSelectors() {
		stored, _ := s.StoredName()
		note := "read only, its concurrency is fixed"
		if s.Adjustable() {
			note = "its concurrency can be changed"
		}
		out += fmt.Sprintf("  %s (stored as %s; %s)\n", s, stored, note)
	}
	return out + "\nEither form is accepted."
}

func init() {
	queueConcurrencyCmd.AddCommand(queueConcurrencyGetCmd)
	queueConcurrencyCmd.AddCommand(queueConcurrencySetCmd)
	queueCmd.AddCommand(queueConcurrencyCmd)
	rootCmd.AddCommand(queueCmd)
}
