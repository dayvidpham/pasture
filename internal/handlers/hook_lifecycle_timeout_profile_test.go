package handlers

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// TestLifecycleReceiptServiceForHonoursTheSuppliedProfile pins the branch that
// carries an operator's tuned timeout profile into the receipt path.
//
// The appender's deadline is the profile's Ingress tier and the reclaim window
// is its WorkflowResult tier. If the supplied profile were ignored, an operator
// who lowered the ingress window would see the hook deadline move while the
// receipt append kept its old budget — a knob that reports a change it did not
// make. The zero profile must still select the production windows, so every
// caller that does not tune anything is unaffected.
func TestLifecycleReceiptServiceForHonoursTheSuppliedProfile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()

	def, err := lifecycleReceiptServiceFor(tracker, commitOrderClock{}, commitOrderOperations{}, timeouts.Profile{})
	require.NoError(t, err)
	require.Equal(t, timeouts.ProductionProfile().WorkflowResult(), def.Window,
		"the zero profile must keep the production reclaim window")
	require.Equal(t, timeouts.ProductionProfile().Ingress(), def.Appender.Deadline,
		"the zero profile must keep the production receipt-appender deadline")

	tuned := timeouts.DeadlineTestProfile()
	got, err := lifecycleReceiptServiceFor(tracker, commitOrderClock{}, commitOrderOperations{}, tuned)
	require.NoError(t, err)
	require.Equal(t, tuned.WorkflowResult(), got.Window,
		"a supplied profile's WorkflowResult tier must reach the reclaim window")
	require.Equal(t, tuned.Ingress(), got.Appender.Deadline,
		"a supplied profile's Ingress tier must reach the receipt appender")
}
