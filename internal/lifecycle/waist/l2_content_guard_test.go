package waist_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// This file is the permanent structural guard on what an L2 may carry. The
// requirement it holds is the one the user ruled on after payload lowering was
// withdrawn: an L2 carries only its kind, its semantic, its declared
// correlation identities, its ordering and its declared structured signals. It
// never carries tool arguments, tool output or prompt text.
//
// The guard is DATA-DRIVEN. Its population is every row of every pinned
// lifecycle profile, read from the contracts' own event lists, tied to every
// row of every generated registration manifest. A row added to a profile or a
// manifest is covered the moment it exists; no edit here is needed, and a
// missing row on either side is refused, so the population cannot shrink in
// silence. The mutation that distinguishes this from a hand list is to
// register a new event carrying a free-text identity and watch the guard turn
// RED with no change to this file.
//
// The only door content has into an L2 is a declared identity, because the
// interpreted record is derived from the L2 and nothing else. So the guard
// stands on two checks: what a profile may DECLARE as an identity, and what
// the derived records may CARRY on the wire.

// interpretedMembers is the closed member set of one interpreted record.
var interpretedMembers = []string{"semantic", "identities", "unresolved_facts", "contract", "manifest"}

// interpretedIdentityMembers, interpretedUnresolvedMembers and
// interpretedManifestMembers are the closed member sets of the nested objects.
var (
	interpretedIdentityMembers   = []string{"kind", "value"}
	interpretedUnresolvedMembers = []string{"reason"}
	interpretedManifestMembers   = []string{"id", "version", "content"}
)

// consultationMembers and its nested sets are the closed member sets of the
// consultation record a gate row derives beside its interpreted record.
var (
	consultationMembers            = []string{"legalized", "response", "interpreted"}
	consultationInterpretedMembers = []string{"result_slot", "content_digest"}
	consultationLegalizedMembers   = []string{"authority"}
	consultationResponseMembers    = []string{"decision"}
)

// identifierFieldName is the shape every native correlation field the hosts
// expose has: a bare identifier that ends in "id" (session_id, tool_use_id,
// request_id, agent_id, turn_id, sessionID, callID, messageID). A field that
// does not end in id is content, not correlation: prompt, tool_input,
// tool_response, message, output. The rule is deliberately narrow so that a
// new legitimate identity name that does not fit it fails loudly here and is
// widened on purpose, by an edit to this rule that the failure names, rather
// than a content field passing quietly.
var identifierFieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func isIdentifierFieldName(name string) bool {
	return identifierFieldName.MatchString(name) && strings.HasSuffix(strings.ToLower(name), "id")
}

// guardRow is one pinned profile row with its bound L1.
type guardRow struct {
	harness    ir.HarnessID
	nativeName string
	mapping    runtime.LifecycleEventMapping
	l1         waist.L1
}

func (r guardRow) name() string { return string(r.harness) + "/" + r.nativeName }

func collectGuardRows[E comparable](t *testing.T, contract runtime.LifecycleContract[E]) []guardRow {
	t.Helper()
	events := contract.Events()
	require.NotEmpty(t, events, "the %s lifecycle contract declared no events, so this guard would check nothing for it", contract.Harness())
	rows := make([]guardRow, 0, len(events))
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		l1, err := waist.BindEvent(contract, event)
		require.NoError(t, err)
		rows = append(rows, guardRow{harness: contract.Harness(), nativeName: mapping.NativeName(), mapping: mapping, l1: l1})
	}
	return rows
}

// pinnedProfileRows is the derived population: every row of the three pinned
// lifecycle profiles.
func pinnedProfileRows(t *testing.T) []guardRow {
	t.Helper()
	rows := collectGuardRows(t, runtime.ClaudeCode2_1_261Lifecycle())
	rows = append(rows, collectGuardRows(t, runtime.Codex0_153_0Lifecycle())...)
	rows = append(rows, collectGuardRows(t, runtime.OpenCode1_18_29Lifecycle())...)
	return rows
}

// registrationRows is the other half of the population: every row of the
// three generated registration manifests, keyed the same way.
func registrationRows(t *testing.T) map[string]registration.Event {
	t.Helper()
	rows := map[string]registration.Event{}
	for _, manifest := range []registration.Manifest{registration.ClaudeCode2_1_261(), registration.Codex0_153_0(), registration.OpenCode1_18_29()} {
		entries := manifest.Entries()
		require.NotEmpty(t, entries, "the %s registration manifest declared no events, so this guard would check nothing for it", manifest.Harness)
		for _, event := range entries {
			key := string(manifest.Harness) + "/" + event.NativeName
			_, duplicate := rows[key]
			require.False(t, duplicate, "the %s registration manifest lists %s twice", manifest.Harness, event.NativeName)
			rows[key] = event
		}
	}
	return rows
}

// TestEveryProfileIdentityFieldIsAnIdentifierName is the declaration guard: a
// profile row may declare as an identity only a field whose name is an
// identifier name and whose kind is a declared correlation kind. A row that
// declares prompt, tool_input, tool_response or any other content field as an
// identity turns this RED, naming the row and the field.
func TestEveryProfileIdentityFieldIsAnIdentifierName(t *testing.T) {
	t.Parallel()
	for _, row := range pinnedProfileRows(t) {
		row := row
		t.Run(row.name(), func(t *testing.T) {
			t.Parallel()
			for _, field := range row.mapping.Identities() {
				assert.True(t, field.Kind().IsValid(),
					"row %s declares identity field %q with kind %d, which is not a declared correlation kind", row.name(), field.NativeName(), uint8(field.Kind()))
				assert.True(t, isIdentifierFieldName(field.NativeName()),
					"row %s declares %q as a correlation identity, but that name is not an identifier name (a bare identifier ending in id); a field with this name carries content, and content never enters an L2", row.name(), field.NativeName())
			}
		})
	}
}

// TestEveryProfileRowIsTiedToOneRegistrationRow ties the two tables that
// describe an event. Every profile row has exactly one registration row with
// the same native name and the other way round, so neither table can grow a
// row the other does not know. On identities the tie is DIRECTIONAL, because
// the two tables are not equally complete today: the registration manifests
// declare identities only for the events whose capture has been proven, and
// are silent on the rest, while the profile declares every row. So: every
// identity the registration declares must exist in the profile with the same
// kind and the same requirement, and the registration may never declare more
// identities than the profile. A free-text identity added to a registration
// row therefore breaks the tie and is named, whether or not the profile has
// it; added to the profile it is refused by the declaration guard above.
//
// The number of rows on which the registration is silent is pinned. A larger
// number is a new silent gap; a smaller one means rows were completed at a pin
// bump, and the number is lowered deliberately in the same change.
func TestEveryProfileRowIsTiedToOneRegistrationRow(t *testing.T) {
	t.Parallel()
	profile := pinnedProfileRows(t)
	registered := registrationRows(t)
	require.Len(t, registered, len(profile),
		"the registration manifests declare %d events and the lifecycle profiles declare %d rows; every event is described by exactly one row on each side", len(registered), len(profile))

	seen := map[string]struct{}{}
	silent := 0
	for _, row := range profile {
		seen[row.name()] = struct{}{}
		event, found := registered[row.name()]
		require.True(t, found, "profile row %s has no registration row; a profile row with no registered event describes nothing a host can send", row.name())
		declared := row.mapping.Identities()
		if len(event.Identities) == 0 && len(declared) > 0 {
			silent++
		}
		require.LessOrEqual(t, len(event.Identities), len(declared),
			"registration row %s declares %d identities but its profile row declares %d; a registration may not lift a field into an L2 that the profile does not declare as correlation", row.name(), len(event.Identities), len(declared))
		consumed := make([]bool, len(declared))
		for index, identity := range event.Identities {
			assert.True(t, identity.Binding.IsValid(),
				"registration row %s identity %d has binding kind %d, which is not a declared kind", row.name(), index, uint8(identity.Binding))
			matched := false
			for position, field := range declared {
				if consumed[position] || uint8(field.Kind()) != uint8(identity.Binding) {
					continue
				}
				consumed[position] = true
				matched = true
				assert.Equal(t, field.Required(), identity.Required,
					"registration row %s identity %d and profile field %q disagree on whether the identity is required", row.name(), index, field.NativeName())
				break
			}
			assert.True(t, matched,
				"registration row %s identity %d is bound as kind %d, and the profile row declares no correlation field of that kind; the registration would lift a field into an L2 that the profile does not declare", row.name(), index, uint8(identity.Binding))
		}
	}
	for key := range registered {
		_, found := seen[key]
		assert.True(t, found, "registration row %s has no lifecycle profile row; an event with no profile row has no semantics", key)
	}
	assert.Equal(t, registrationSilentOnIdentitiesRows, silent,
		"the registration manifests are silent on identities for %d profile rows, want exactly %d; a LARGER number is a new silent gap, a SMALLER one means rows were completed and this number must be lowered deliberately", silent, registrationSilentOnIdentitiesRows)
}

// registrationSilentOnIdentitiesRows is the number of profile rows whose
// registration row declares no identity while the profile declares at least
// one. They are the unproven Codex and OpenCode rows whose ingress catalogue
// has not been completed yet.
const registrationSilentOnIdentitiesRows = 19

// memberNames returns the sorted member names of one JSON object.
func memberNames(t *testing.T, raw json.RawMessage, what string) ([]string, map[string]json.RawMessage) {
	t.Helper()
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &object), "%s is not a JSON object", what)
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, object
}

// syntheticIdentityValue is the value the guard supplies for one declared
// identity. It is identifier-shaped and unique per field, so the wire check
// can tell that the value it reads is the identity it supplied and nothing
// else.
func syntheticIdentityValue(field runtime.NativeIdentityField) string {
	return fmt.Sprintf("%s-guard-value", field.NativeName())
}

// TestEveryProfileRowDerivesOnlyTheClosedWireShape is the wire guard: for every
// row, an L2 built from its declared identities derives an interpreted record,
// and for a gate row a consultation record, whose members are exactly the
// closed sets above and whose identity values are exactly the identities
// supplied. A record that gains a member, such as an inlined body, or an
// identity value that is not the supplied identifier, turns this RED naming the
// row and the member.
func TestEveryProfileRowDerivesOnlyTheClosedWireShape(t *testing.T) {
	t.Parallel()
	for _, row := range pinnedProfileRows(t) {
		row := row
		t.Run(row.name(), func(t *testing.T) {
			t.Parallel()
			declared := row.mapping.Identities()
			identities := make([]waist.Identity, 0, len(declared))
			supplied := map[string]struct{}{}
			for _, field := range declared {
				identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), syntheticIdentityValue(field))
				require.NoError(t, err)
				identities = append(identities, identity)
				supplied[syntheticIdentityValue(field)] = struct{}{}
			}
			l2, err := row.l1.NewEvent(identities)
			require.NoError(t, err)
			derivation, err := middleend.Derive(l2, metamodel.Active())
			require.NoError(t, err)
			effects := derivation.Effects()
			require.NotEmpty(t, effects, "row %s derived no effects", row.name())

			members, object := memberNames(t, effects[0].Payload, "the interpreted record of "+row.name())
			assert.ElementsMatch(t, interpretedMembers, members,
				"the interpreted record of %s carries members %v; the closed set is %v, and a member outside it is content the L2 must not carry", row.name(), members, interpretedMembers)

			var wireIdentities []json.RawMessage
			require.NoError(t, json.Unmarshal(object["identities"], &wireIdentities))
			require.Len(t, wireIdentities, len(declared), "the interpreted record of %s carries %d identities but the row declares %d", row.name(), len(wireIdentities), len(declared))
			for index, raw := range wireIdentities {
				names, identity := memberNames(t, raw, fmt.Sprintf("identity %d of %s", index, row.name()))
				assert.ElementsMatch(t, interpretedIdentityMembers, names,
					"identity %d of %s carries members %v; the closed set is %v", index, row.name(), names, interpretedIdentityMembers)
				var value string
				require.NoError(t, json.Unmarshal(identity["value"], &value))
				_, wasSupplied := supplied[value]
				assert.True(t, wasSupplied, "identity %d of %s carries value %q, which is not one of the identifiers supplied; an L2 identity value is the host's identifier and nothing else", index, row.name(), value)
			}
			var wireUnresolved []json.RawMessage
			require.NoError(t, json.Unmarshal(object["unresolved_facts"], &wireUnresolved))
			for index, raw := range wireUnresolved {
				names, _ := memberNames(t, raw, fmt.Sprintf("unresolved fact %d of %s", index, row.name()))
				assert.ElementsMatch(t, interpretedUnresolvedMembers, names,
					"unresolved fact %d of %s carries members %v; the closed set is %v", index, row.name(), names, interpretedUnresolvedMembers)
			}
			manifestMembers, _ := memberNames(t, object["manifest"], "the manifest of "+row.name())
			assert.ElementsMatch(t, interpretedManifestMembers, manifestMembers,
				"the manifest of %s carries members %v; the closed set is %v", row.name(), manifestMembers, interpretedManifestMembers)

			if row.mapping.Semantic() != runtime.SemanticGateConsultation {
				require.Len(t, effects, 1, "row %s is not a gate consultation and must derive exactly one record", row.name())
				return
			}
			require.Len(t, effects, 2, "gate row %s must derive exactly the interpreted record and the consultation record", row.name())
			members, object = memberNames(t, effects[1].Payload, "the consultation record of "+row.name())
			assert.ElementsMatch(t, consultationMembers, members,
				"the consultation record of %s carries members %v; the closed set is %v", row.name(), members, consultationMembers)
			nested, _ := memberNames(t, object["interpreted"], "the interpreted reference of "+row.name())
			assert.ElementsMatch(t, consultationInterpretedMembers, nested, "the interpreted reference of %s carries members %v; the closed set is %v", row.name(), nested, consultationInterpretedMembers)
			nested, _ = memberNames(t, object["legalized"], "the legalized member of "+row.name())
			assert.ElementsMatch(t, consultationLegalizedMembers, nested, "the legalized member of %s carries members %v; the closed set is %v", row.name(), nested, consultationLegalizedMembers)
			nested, _ = memberNames(t, object["response"], "the response member of "+row.name())
			assert.ElementsMatch(t, consultationResponseMembers, nested, "the response member of %s carries members %v; the closed set is %v", row.name(), nested, consultationResponseMembers)
		})
	}
}
