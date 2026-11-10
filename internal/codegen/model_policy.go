// Package codegen — capability-tier model policy.
//
// The policy is deliberately provider-neutral. A role names a capability tier;
// a tier names a neutral default model nickname, an effort level, and the roles
// it maps by default. The emitters translate the effective tier to each
// harness's native model id. Prose never names a vendor model; only emitted
// agent files carry a native id.
package codegen

import (
	"fmt"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// CapabilityTier is the closed capability-tier set. T1 is the most capable
// tier, T2 the standard tier, and T3 the smallest tier.
type CapabilityTier string

const (
	// TierFrontier is the frontier-capability tier (T1).
	TierFrontier CapabilityTier = "T1"
	// TierStandard is the standard-capability tier (T2).
	TierStandard CapabilityTier = "T2"
	// TierMini is the mini-capability tier (T3).
	TierMini CapabilityTier = "T3"
)

var canonicalCapabilityTiers = []CapabilityTier{TierFrontier, TierStandard, TierMini}

// AllCapabilityTiers returns a fresh copy of the closed tier set in ascending
// capability order. A caller may not mutate the registry through the result.
func AllCapabilityTiers() []CapabilityTier {
	return append([]CapabilityTier(nil), canonicalCapabilityTiers...)
}

// IsValid reports whether the value is a registered capability tier.
func (t CapabilityTier) IsValid() bool {
	for _, candidate := range canonicalCapabilityTiers {
		if candidate == t {
			return true
		}
	}
	return false
}

// EffortLevel is the closed effort set attached to a capability tier.
type EffortLevel string

const (
	// EffortHigh is the effort level for the frontier tier.
	EffortHigh EffortLevel = "high"
	// EffortMedium is the effort level for the standard tier.
	EffortMedium EffortLevel = "medium"
	// EffortLow is the effort level for the mini tier.
	EffortLow EffortLevel = "low"
)

var canonicalEffortLevels = []EffortLevel{EffortHigh, EffortMedium, EffortLow}

// AllEffortLevels returns a fresh copy of the closed effort set.
func AllEffortLevels() []EffortLevel {
	return append([]EffortLevel(nil), canonicalEffortLevels...)
}

// IsValid reports whether the value is a registered effort level.
func (e EffortLevel) IsValid() bool {
	for _, candidate := range canonicalEffortLevels {
		if candidate == e {
			return true
		}
	}
	return false
}

// CapabilityTierSpec is one row of the capability-tier registry. DefaultModel
// is a neutral nickname, never a vendor id. Roles is the default role mapping;
// a role may override its tier through RoleSpec.TierOverride.
type CapabilityTierSpec struct {
	Tier         CapabilityTier
	Name         string
	DefaultModel string
	Effort       EffortLevel
	Roles        []protocol.RoleId
}

// CapabilityTierSpecs is the one capability-tier registry. Every tier carries a
// default model nickname, an effort level, and its default role mapping.
var CapabilityTierSpecs = map[CapabilityTier]CapabilityTierSpec{
	TierFrontier: {
		Tier:         TierFrontier,
		Name:         "frontier",
		DefaultModel: "frontier",
		Effort:       EffortHigh,
		Roles: []protocol.RoleId{
			protocol.RoleEpoch,
			protocol.RoleArchitect,
			protocol.RoleSupervisor,
		},
	},
	TierStandard: {
		Tier:         TierStandard,
		Name:         "standard",
		DefaultModel: "standard",
		Effort:       EffortMedium,
		Roles:        []protocol.RoleId{protocol.RoleWorker},
	},
	TierMini: {
		Tier:         TierMini,
		Name:         "mini",
		DefaultModel: "mini",
		Effort:       EffortLow,
		Roles:        []protocol.RoleId{protocol.RoleReviewer},
	},
}

// MaxConcurrentAgents is the concurrency cap for one stage of a workflow. It
// bounds how many delegated agents run at once, independent of the review
// round bound (maxRounds).
//
// Four is the widest concurrent delegation the protocol creates: one wave
// dispatches three standing reviewers plus the supervisor that sends fixes, so
// a lower cap would serialize review and a higher one would admit a fifth
// writer to the same store for no phase the protocol defines. The number lives
// here only; rendered prose interpolates it instead of repeating the literal.
const MaxConcurrentAgents = 4

// concurrencyCapSentence is the one rendered sentence for the cap. Callers
// concatenate it rather than repeating the number, so the constant is the only
// home for the literal.
func concurrencyCapSentence() string {
	return fmt.Sprintf("respect the concurrency cap of %d.", MaxConcurrentAgents)
}

// mustCapabilityTierSpec returns a registered tier row or panics. It is for
// render/build paths that run after ValidateCapabilityTiers has accepted the
// registry; a miss is a programming error, not user input.
func mustCapabilityTierSpec(tier CapabilityTier) CapabilityTierSpec {
	spec, ok := CapabilityTierSpecs[tier]
	if !ok {
		panic(fmt.Sprintf("codegen: capability tier %q is not registered", tier))
	}
	return spec
}

// EffectiveTier returns the tier a role actually uses: its explicit override
// when one is set, otherwise its mapped tier. The reviewer tier is the reviewer
// role's override; there is no separate review-budget type.
func (s RoleSpec) EffectiveTier() CapabilityTier {
	if s.TierOverride != "" {
		return s.TierOverride
	}
	return s.Tier
}

// TierForRole resolves a role's effective capability tier. It fails closed when
// the role is absent or its effective tier is not registered.
func TierForRole(roleID protocol.RoleId) (CapabilityTier, error) {
	spec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.TierForRole: role %q not found in RoleSpecs — "+
				"where: internal/codegen/specs_data.go — "+
				"fix: define the role in RoleSpecs with a registered Tier",
			roleID,
		)
	}
	tier := spec.EffectiveTier()
	if !tier.IsValid() {
		return "", fmt.Errorf(
			"codegen.TierForRole: role %q resolves to unregistered capability tier %q — "+
				"where: internal/codegen/specs_data.go RoleSpecs[%q] — "+
				"fix: set Tier to one of %v or remove the invalid TierOverride",
			roleID, tier, roleID, AllCapabilityTiers(),
		)
	}
	return tier, nil
}

// tierModels is the one tier -> native model id table per harness. Vendor ids
// live here and in emitted agent files, never in prose.
var tierModels = map[HarnessName]map[CapabilityTier]string{
	HarnessClaudeCode: {
		TierFrontier: "claude-opus-4-8",
		TierStandard: "claude-sonnet-4-6",
		TierMini:     "claude-haiku-4-5",
	},
	HarnessOpenCode: {
		TierFrontier: "anthropic/claude-opus-4-8",
		TierStandard: "anthropic/claude-sonnet-4-6",
		TierMini:     "anthropic/claude-haiku-4-5",
	},
	HarnessCodex: {
		TierFrontier: "gpt-5.6-sol",
		TierStandard: "gpt-5.6-terra",
		// Codex exposes two documented dispatch profiles in this tree. The
		// mini tier shares the standard profile rather than inventing an
		// unverified third id.
		TierMini: "gpt-5.6-terra",
	},
}

// ModelForTier resolves one capability tier to a harness's native model id.
// It fails closed on an unregistered harness or tier so an emitter can never
// publish an empty or wrong model field.
func ModelForTier(harness HarnessName, tier CapabilityTier) (string, error) {
	byTier, ok := tierModels[harness]
	if !ok {
		return "", fmt.Errorf(
			"codegen.ModelForTier: harness %q has no model tier table — "+
				"where: internal/codegen/model_policy.go tierModels — "+
				"fix: add a tier->id row for harness %q",
			harness, harness,
		)
	}
	model, ok := byTier[tier]
	if !ok || model == "" {
		return "", fmt.Errorf(
			"codegen.ModelForTier: capability tier %q has no %s model id — "+
				"where: internal/codegen/model_policy.go tierModels[%q] — "+
				"fix: map tier %q to a native %s model id",
			tier, harness, harness, tier, harness,
		)
	}
	return model, nil
}

// EffortForTier returns the effort level a tier carries. It fails closed on an
// unregistered tier.
func EffortForTier(tier CapabilityTier) (EffortLevel, error) {
	spec, ok := CapabilityTierSpecs[tier]
	if !ok {
		return "", fmt.Errorf(
			"codegen.EffortForTier: capability tier %q is not registered — "+
				"where: internal/codegen/model_policy.go CapabilityTierSpecs — "+
				"fix: register the tier or use AllCapabilityTiers",
			tier,
		)
	}
	return spec.Effort, nil
}

// ValidateCapabilityTiers proves the tier registry and role mapping are closed,
// complete, and free of duplicate role ownership.
func ValidateCapabilityTiers() error {
	return validateCapabilityTiers(canonicalCapabilityTiers, CapabilityTierSpecs, RoleSpecs)
}

// validateCapabilityTiers is the pure core of the registry guard. It accepts
// the three registries so a two-sample test can prove a defective copy fails.
func validateCapabilityTiers(tiers []CapabilityTier, tierSpecs map[CapabilityTier]CapabilityTierSpec, roleSpecs map[protocol.RoleId]RoleSpec) error {
	seenRoles := make(map[protocol.RoleId]CapabilityTier, len(roleSpecs))
	for _, tier := range tiers {
		spec, ok := tierSpecs[tier]
		if !ok {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: tier %q is in the closed set but has no CapabilityTierSpecs row — "+
					"where: internal/codegen/model_policy.go — fix: register the tier",
				tier,
			)
		}
		if spec.Tier != tier {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: row key %q carries Tier %q — "+
					"where: internal/codegen/model_policy.go CapabilityTierSpecs — fix: make the row key and Tier agree",
				tier, spec.Tier,
			)
		}
		if spec.Name == "" || spec.DefaultModel == "" {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: tier %q is missing a name or default model nickname — "+
					"where: internal/codegen/model_policy.go CapabilityTierSpecs — fix: set Name and DefaultModel",
				tier,
			)
		}
		if !spec.Effort.IsValid() {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: tier %q carries unregistered effort %q — "+
					"where: internal/codegen/model_policy.go CapabilityTierSpecs — fix: use AllEffortLevels",
				tier, spec.Effort,
			)
		}
		for _, roleID := range spec.Roles {
			if prior, dup := seenRoles[roleID]; dup {
				return fmt.Errorf(
					"codegen.ValidateCapabilityTiers: role %q is mapped by both %q and %q — "+
						"where: internal/codegen/model_policy.go CapabilityTierSpecs — fix: assign the role exactly one tier",
					roleID, prior, tier,
				)
			}
			seenRoles[roleID] = tier
			roleSpec, ok := roleSpecs[roleID]
			if !ok {
				return fmt.Errorf(
					"codegen.ValidateCapabilityTiers: tier %q maps unknown role %q — "+
						"where: internal/codegen/model_policy.go CapabilityTierSpecs — fix: map a role defined in RoleSpecs",
					tier, roleID,
				)
			}
			if roleSpec.Tier != tier {
				return fmt.Errorf(
					"codegen.ValidateCapabilityTiers: tier %q maps role %q but RoleSpec.Tier is %q — "+
						"where: internal/codegen/specs_data.go RoleSpecs[%q] — fix: make the role mapping and role spec agree",
					tier, roleID, roleSpec.Tier, roleID,
				)
			}
		}
	}
	for roleID, spec := range roleSpecs {
		effective := spec.EffectiveTier()
		if !effective.IsValid() {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: role %q has unregistered effective tier %q — "+
					"where: internal/codegen/specs_data.go RoleSpecs[%q] — fix: set Tier or TierOverride to a registered tier",
				roleID, effective, roleID,
			)
		}
		if spec.TierOverride != "" && !spec.TierOverride.IsValid() {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: role %q has unregistered TierOverride %q — "+
					"where: internal/codegen/specs_data.go RoleSpecs[%q] — fix: use AllCapabilityTiers",
				roleID, spec.TierOverride, roleID,
			)
		}
		// Both directions: the tiers->roles loop above proves every listed role
		// agrees with its RoleSpec. This proves every role's DEFAULT tier lists
		// it, so deleting a role from TierFrontier.Roles cannot leave the role
		// mapped by a tier that no longer advertises it.
		tierSpec, ok := tierSpecs[spec.Tier]
		if !ok {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: role %q maps to tier %q with no CapabilityTierSpecs row — "+
					"where: internal/codegen/model_policy.go — fix: register the tier",
				roleID, spec.Tier,
			)
		}
		if !roleListed(tierSpec.Roles, roleID) {
			return fmt.Errorf(
				"codegen.ValidateCapabilityTiers: role %q defaults to tier %q but that tier's Roles does not list it — "+
					"where: internal/codegen/model_policy.go CapabilityTierSpecs[%q].Roles — fix: add %q to the tier's role list or change RoleSpecs[%q].Tier",
				roleID, spec.Tier, spec.Tier, roleID, roleID,
			)
		}
	}
	return nil
}

// roleListed reports whether roles contains roleID.
func roleListed(roles []protocol.RoleId, roleID protocol.RoleId) bool {
	for _, candidate := range roles {
		if candidate == roleID {
			return true
		}
	}
	return false
}
