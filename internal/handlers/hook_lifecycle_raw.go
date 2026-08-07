package handlers

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

const hookLifecycleRawWhere = "Ingesting a raw lifecycle event (internal/handlers/hook_lifecycle_raw.go in handlers.HookLifecycleRaw)."

// RawSchemaVersion is the wire-level schema identity of a raw lifecycle
// payload (URD R4.1, UAT-Q2). The decoder is pinned to the build: the
// identity selects the exact generated registration the payload conforms to,
// and an unknown value is a typed refusal (never a silent default decode).
//
// Values are the exact runtime contract spellings of the three registered,
// verified frontends (harness/version), mirroring what
// ir.NewRuntimeContractID(harness, manifest.Version) yields for each row. The
// closed set is pinned by tests against the generated registrations, so the
// enum cannot drift from the builds it names.
type RawSchemaVersion string

const (
	// RawSchemaClaudeCode2_1_210 is the wire identity of the Claude Code
	// 2.1.210 payload schema pinned in this build.
	RawSchemaClaudeCode2_1_210 RawSchemaVersion = "claude-code/2.1.210"
	// RawSchemaOpenCode1_18_10 is the wire identity of the OpenCode 1.18.10
	// payload schema pinned in this build.
	RawSchemaOpenCode1_18_10 RawSchemaVersion = "opencode/1.18.10"
	// RawSchemaCodex0_146_0 is the wire identity of the Codex 0.146.0 payload
	// schema pinned in this build.
	RawSchemaCodex0_146_0 RawSchemaVersion = "codex/0.146.0"
)

// IsValid reports whether the schema identity names a version pinned in this
// build.
func (v RawSchemaVersion) IsValid() bool {
	switch v {
	case RawSchemaClaudeCode2_1_210, RawSchemaOpenCode1_18_10, RawSchemaCodex0_146_0:
		return true
	default:
		return false
	}
}

// String returns the canonical wire spelling.
func (v RawSchemaVersion) String() string { return string(v) }

// ParseRawSchemaVersion validates a user-supplied --schema-version value
// against the closed set of wire identities pinned in this build. The
// returned error is actionable: it names the offending value and the set of
// identities this build decodes.
func ParseRawSchemaVersion(value string) (RawSchemaVersion, error) {
	candidate := RawSchemaVersion(strings.TrimSpace(value))
	if !candidate.IsValid() {
		return "", fmt.Errorf("wire schema %q is not known to this build of pasture; supply one of %q, %q, or %q",
			value, RawSchemaClaudeCode2_1_210, RawSchemaOpenCode1_18_10, RawSchemaCodex0_146_0)
	}
	return candidate, nil
}

// uuid hookLifecycleRawInput mirrors HookLifecycleInput for the raw ingestion
// hatch: the same coordinates (db path, harness, event, host version, stdin)
// plus the explicitly typed wire-level schema identity. It is a fresh type so
// the raw surface can never be reduced accidentally by callers of the native
// entrypoint.
type HookLifecycleRawInput struct {
	DBPath        string
	Harness       ir.HarnessID
	Event         string
	HostVersion   string
	SchemaVersion RawSchemaVersion
	Input         io.Reader
	Clock         receipt.Clock
	Operations    receipt.OperationIDSource
	Activations   []activation.Entry
}

// rawSchemaVersionFor returns the wire schema identity pinned to the given
// harness by this build's generated registration. The identity is derived, not
// hand-maintained: it is ir.NewRuntimeContractID(harness, version) rendered
// canonically, so a build can never advertise a schema identity its own
// registrations do not decode.
func rawSchemaVersionFor(harness ir.HarnessID) RawSchemaVersion {
	var version string
	switch harness {
	case ir.HarnessClaudeCode:
		version = registration.ClaudeCode2_1_210().Version
	case ir.HarnessOpenCode:
		version = registration.OpenCode1_18_10().Version
	case ir.HarnessCodex:
		version = registration.Codex0_146_0().Version
	default:
		return ""
	}
	contract, err := ir.NewRuntimeContractID(harness, version)
	if err != nil {
		return ""
	}
	return RawSchemaVersion(contract.String())
}

// rawAcknowledge is the canonical proceed acknowledgement bytes the raw hatch
// writes to stdout ONLY after the committed receipt returns (commit-before-
// stdout structural, mirroring HookLifecycleNative). The spellings are
// byte-identical to the native continuation shapes the same harness reads
// after a gate proceed (nativersponse): {"decision":"proceed"} for the
// canonical Claude/OpenCode host response and {"continue":true} for the Codex
// command-hook continuation.
func rawAcknowledge(harness ir.HarnessID) []byte {
	switch harness {
	case ir.HarnessCodex:
		return []byte(`{"continue":true}`)
	default:
		return []byte(`{"decision":"proceed"}`)
	}
}

// HookLifecycleRaw admits a raw lifecycle payload through the SAME gate path
// as native (NewDeliveryIntent → Legalize → service.Receive), stamps the
// occurrence with the raw origin, and only on the nil-error path returns the
// canonical proceed bytes the command writes to stdout. An unknown harness,
// unknown wire schema, malformed stdin, or over-limit stdin refuses BEFORE the
// store opens, preserving the M1 §8 property that an invalid invocation
// creates no database file.
//
// Implemented in SLICE-2-L3. The L1 signature is the frozen contract; the
// body is replaced by the implementation layer.
func HookLifecycleRaw(_ context.Context, _ HookLifecycleRawInput) ([]byte, error) {
	return nil, fmt.Errorf("HookLifecycleRaw is not implemented yet (SLICE-2-L3)")
}
