package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// queryCmd groups the read-only epoch-state queries. Each query reads the
// EpochState projection from the local pasture database and recomputes the
// reachable transitions through the protocol state machine — no running daemon
// is contacted.
var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Read epoch state from the local projection",
	Long: `Read the recorded state of an epoch from the local pasture database.

Queries read the persisted epoch-state projection (a SQL read) and recompute
the reachable transitions through the protocol state machine. They do not
contact a running daemon and do not change any state.`,
}

// runQuery is the shared body for every query subcommand: it resolves the
// epoch id + db path from flags and delegates to the projection reader.
func runQuery(cmd *cobra.Command, query protocol.QueryName) error {
	epochId, _ := cmd.Flags().GetString("epoch-id")
	code, err := handlers.QueryEpoch(handlers.QueryEpochInput{
		DBPath:  flagDBPath,
		EpochId: epochId,
		Query:   query,
	}, resolveFormat())
	if err != nil {
		printError(err)
	}
	if code != 0 {
		exitWithCode(code)
	}
	return nil
}

var queryStateCmd = &cobra.Command{
	Use:   "state",
	Short: "Show the full recorded state of an epoch",
	Long: `Show the full recorded state of an epoch: current phase and role, vote
history, recorded transitions, available transitions, and active session count.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQuery(cmd, protocol.QueryFullState)
	},
}

var queryCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current phase and role of an epoch",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQuery(cmd, protocol.QueryCurrentState)
	},
}

var queryTransitionsCmd = &cobra.Command{
	Use:   "transitions",
	Short: "Show the transitions currently reachable from an epoch's phase",
	Long: `Show the phases reachable from the epoch's current phase, after applying the
consensus, revise, and blocker gates to its current vote and blocker state.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQuery(cmd, protocol.QueryAvailableTransitions)
	},
}

func init() {
	for _, c := range []*cobra.Command{queryStateCmd, queryCurrentCmd, queryTransitionsCmd} {
		c.Flags().String("epoch-id", "", "Epoch ID to query (required)")
		_ = c.MarkFlagRequired("epoch-id")
		queryCmd.AddCommand(c)
	}
	rootCmd.AddCommand(queryCmd)
}
