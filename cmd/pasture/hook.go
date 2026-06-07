package main

import (
	"fmt"

	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// hookCmd is the parent for hook-event recording subcommands. It records
// Claude Code / git lifecycle hook events into the unified pasture audit trail
// WITHOUT requiring the pastured daemon.
var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Record lifecycle hook events into the audit trail",
	Long: `Record Claude Code / git lifecycle hook events into the unified pasture
audit trail.

Hook events are dispatched through the same in-process pipeline the pastured
daemon uses (hooks.Manager → recorder → audit trail), so the CLI works with or
without the daemon running.`,
}

// hookRecordCmd implements `pasture hook record`.
var hookRecordCmd = &cobra.Command{
	Use:   "record",
	Short: "Record a hook event (e.g. a git commit) into the audit trail",
	Long: `Record a hook event into the unified pasture audit trail.

The --event flag selects what to record (currently: git-commit). For a
git-commit, --sha is required and identifies the commit; the optional metadata
flags (--message, --author, --branch, --timestamp) override values otherwise
derived best-effort from git.

When git is consulted (i.e. at least one of the four commit flags is omitted),
the repository identity is also captured: --repo overrides the derived
owner/name slug, and --remote supplies remotes instead of the git-derived map.

Example:
  pasture hook record --event git-commit --sha $(git rev-parse HEAD)
  pasture hook record --event git-commit --sha abc123 --message "fix: bug" --branch main
  pasture hook record --event git-commit --sha abc123 \
    --repo myorg/myrepo \
    --remote origin=git@github.com:myorg/myrepo.git

The recorded event is queryable via:
  pasture task events --context-kind GitContext --context-id <sha>`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		event, _ := cmd.Flags().GetString("event")
		sha, _ := cmd.Flags().GetString("sha")

		in := handlers.HookRecordInput{
			DBPath: flagDBPath,
			Event:  event,
			SHA:    sha,
		}

		// Optional metadata flags: pass a pointer only when explicitly set so
		// the handler can distinguish "absent" (git may fill) from "set empty".
		if cmd.Flags().Changed("message") {
			v, _ := cmd.Flags().GetString("message")
			in.Message = &v
		}
		if cmd.Flags().Changed("author") {
			v, _ := cmd.Flags().GetString("author")
			in.Author = &v
		}
		if cmd.Flags().Changed("branch") {
			v, _ := cmd.Flags().GetString("branch")
			in.Branch = &v
		}
		if cmd.Flags().Changed("timestamp") {
			v, _ := cmd.Flags().GetString("timestamp")
			in.Timestamp = &v
		}
		if cmd.Flags().Changed("repo") {
			v, _ := cmd.Flags().GetString("repo")
			in.Repo = &v
		}
		if cmd.Flags().Changed("remote") {
			v, _ := cmd.Flags().GetStringToString("remote")
			in.Remotes = v
		}

		result, code, hErr := handlers.HookRecord(in)
		if hErr != nil {
			printError(hErr)
		}
		if code != 0 {
			exitWithCode(code)
		}
		if hErr != nil {
			return nil
		}

		// Render the success result under the global --format flag (text or json).
		out, fErr := formatters.FormatHookRecord(
			result.EventType, result.SHA, result.EventID,
			result.Message, result.Author, result.Branch, result.Timestamp,
			result.Repo, result.Remotes,
			resolveFormat(),
		)
		if fErr != nil {
			printError(fErr)
			exitWithCode(pasterrors.ExitCode(fErr))
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), out)
		return nil
	},
}

func init() {
	f := hookRecordCmd.Flags()
	f.String("event", "", "Hook event to record (required). Supported: git-commit")
	f.String("sha", "", "Git commit SHA (required for git-commit)")
	f.String("message", "", "Commit message (optional; overrides git-derived value)")
	f.String("author", "", "Commit author (optional; overrides git-derived value)")
	f.String("branch", "", "Branch name (optional; overrides git-derived value)")
	f.String("timestamp", "", "Commit timestamp (optional; overrides git-derived value)")
	f.String("repo", "", "Repository slug owner/name (optional; overrides git-derived value)")
	f.StringToString("remote", nil, "Remote name and URL as name=url (repeatable; overrides git-derived remotes)")

	hookCmd.AddCommand(hookRecordCmd)
	rootCmd.AddCommand(hookCmd)
}
