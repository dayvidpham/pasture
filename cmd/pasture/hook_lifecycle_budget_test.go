package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/assert"
)

// TestTheOperatorBudgetOverrideReachesTheLifecyclePath drives the PRODUCTION
// lifecycle entry point (lifecycleOutcome) under the operator environment knob
// and pins the three facts the knob promises:
//
//   - unset and set-but-empty both leave the default tiers in force, so the
//     invocation behaves exactly as it did before the knob existed;
//   - a valid override is accepted and the invocation proceeds to evaluate the
//     event, so the knob is wired into the path and not merely parsed;
//   - an inverted override is refused LOUDLY with the existing invariant error,
//     before any store work, as a fail-open fault rather than a silent clamp or
//     a blocked session.
//
// It is serial: it sets process environment (t.Setenv) and drives the shared
// production command, so it may not use t.Parallel. The classification walk in
// TestEveryTestThatReachesSharedProcessStateStaysSerial holds that.
//
// WHAT IT VISITS: the three knob states below — unset, a valid override and an
// inverted override — driven through the production entry point.
// WHAT IT DOES NOT READ: any other event or harness, and the deadline arm
// itself, because a refused configuration stops before the event is read; the
// substitution arithmetic for the tiers is pinned in internal/timeouts.
func TestTheOperatorBudgetOverrideReachesTheLifecyclePath(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantRefused bool
	}{
		{name: "unset knob keeps the default tiers", raw: "", wantRefused: false},
		{name: "valid hook-invocation override is accepted", raw: "3500ms", wantRefused: false},
		{name: "inverted hook-invocation override is refused loudly", raw: "1s", wantRefused: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			// This subject is the invocation knob against the production base.
			// Clear the harness ingress window TestMain pins for child CLI
			// subjects so the "unset" case really sees the production tiers.
			t.Setenv(hookIngressTimeoutEnv, "")
			t.Setenv(hookInvocationTimeoutEnv, tc.raw)

			cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", dbPath)
			// An empty host payload refuses before the store is opened, so this
			// exercises the budget application with no durable work behind it.
			cmd.SetIn(strings.NewReader(""))

			outcome := lifecycleTestOutcome(t, cmd, nil,
				handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

			assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
				"a time-budget refusal is a fault, not a decision, so it must not block the session under the fail-open default")

			if tc.wantRefused {
				assert.Contains(t, outcome.Stderr, "timeout profile is inverted",
					"an inverted override must reach the operator with the existing invariant error")
				assert.Contains(t, outcome.Stderr, hookInvocationTimeoutEnv,
					"the refusal must name the knob that caused it")
				assert.NotContains(t, outcome.Stderr, "standard input carried zero bytes",
					"a refused configuration stops before the event is read, so there is no read fault to report")
				return
			}

			assert.NotContains(t, outcome.Stderr, "timeout profile is inverted")
			assert.Contains(t, outcome.Stderr, "standard input carried zero bytes",
				"an accepted override lets the invocation reach the event, which then fails on the empty payload")
		})
	}
}
