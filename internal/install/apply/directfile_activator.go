package apply

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// DirectFileActivator is the real filesystem implementation for direct-file
// activation. Source controls registry ownership only; paths come exclusively
// from validated activation contracts.
type DirectFileActivator struct {
	policies map[cell.Cell]DirectFilePolicy
}

func NewDirectFileActivator(policies ...DirectFilePolicy) (*DirectFileActivator, error) {
	if len(policies) == 0 {
		return nil, cell.NewFault("direct-file activator construction", "one explicit policy per bound DirectFile cell", "no direct-file policies were provided", "internal/install/apply.NewDirectFileActivator", "constructing strategy dispatch", "direct-file requests would have no cell-specific validation", "pass explicit NewDirectFilePolicy or PassThroughDirectFile values", nil)
	}
	indexed := make(map[cell.Cell]DirectFilePolicy, len(policies))
	for _, policy := range policies {
		if !policy.Cell().IsValid() || policy.validate == nil || policy.decorate == nil {
			return nil, cell.NewFault("direct-file activator construction", "complete validated policies", fmt.Sprintf("policy for %s is invalid", policy.Cell()), "internal/install/apply.NewDirectFileActivator", "indexing policy dispatch", "a request could bypass validation or decoration constraints", "construct every policy with NewDirectFilePolicy", nil)
		}
		if _, exists := indexed[policy.Cell()]; exists {
			return nil, cell.NewFault("direct-file activator construction", "exactly one policy per cell", fmt.Sprintf("cell %s has duplicate policies", policy.Cell()), "internal/install/apply.NewDirectFileActivator", "indexing policy dispatch", "policy selection would be ambiguous", "remove the duplicate policy", nil)
		}
		indexed[policy.Cell()] = policy
	}
	return &DirectFileActivator{policies: indexed}, nil
}

func (*DirectFileActivator) StrategyKind() activation.StrategyKind {
	return activation.DirectFileKindValue()
}

// ValidateBindings proves at service construction that policy dispatch is an
// exact bijection with the statically bound DirectFile cells.
func (a *DirectFileActivator) ValidateBindings(bindings []activation.ComponentActivation) error {
	required := make(map[cell.Cell]struct{}, len(bindings))
	for _, binding := range bindings {
		if !binding.IsValid() || binding.Strategy().Kind() != activation.DirectFileKindValue() {
			return cell.NewFault("direct-file policy binding", "valid DirectFile activation", fmt.Sprintf("cell %s is foreign or not DirectFile", binding.Cell()), "internal/install/apply.DirectFileActivator.ValidateBindings", "validating service construction", "policy coverage cannot be proven", "pass only validated DirectFile component activations", nil)
		}
		required[binding.Cell()] = struct{}{}
	}
	for c := range required {
		if _, ok := a.policies[c]; !ok {
			return cell.NewFault("direct-file policy binding", "one policy for every bound DirectFile cell", fmt.Sprintf("cell %s has no policy", c), "internal/install/apply.DirectFileActivator.ValidateBindings", "validating service construction", "the request would otherwise reach the filesystem without its policy", "register one explicit policy for this cell", nil)
		}
	}
	for c := range a.policies {
		if _, ok := required[c]; !ok {
			return cell.NewFault("direct-file policy binding", "policies only for bound DirectFile cells", fmt.Sprintf("policy cell %s is foreign or bound to another strategy", c), "internal/install/apply.DirectFileActivator.ValidateBindings", "validating service construction", "dead or misrouted policy configuration would be accepted", "remove the foreign policy or bind that cell to DirectFile", nil)
		}
	}
	return nil
}

func (a *DirectFileActivator) strategy(act activation.ComponentActivation) (activation.DirectFile, error) {
	df, ok := act.Strategy().(activation.DirectFile)
	if !ok || !df.IsValid() {
		return activation.DirectFile{}, cell.NewFault("direct-file activation", "valid direct-file strategy", fmt.Sprintf("cell %s is not bound to a valid direct-file strategy", act.Cell()), "internal/install/apply.DirectFileActivator.strategy", "dispatching a direct-file activation", "the wrong controller would inspect or mutate the cell", "bind this cell to a validated direct-file strategy", nil)
	}
	return df, nil
}

// Inspect is read-only and classifies exact current-bundle, exact prior-managed,
// wholly absent, and ambiguous/partial state without creating directories.
func (a *DirectFileActivator) policyRequest(operation Operation, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (DirectFilePolicy, DirectFileRequest, error) {
	request := newDirectFileRequest(operation, source, key, act, prior)
	if !operation.IsValid() || !source.IsValid() || !key.Cell().IsValid() || !act.IsValid() || key.Cell() != act.Cell() || act.Strategy().Kind() != activation.DirectFileKindValue() {
		return DirectFilePolicy{}, request, cell.NewFault("direct-file request validation", "matching valid operation, source, key, activation, and DirectFile strategy", fmt.Sprintf("request for key cell %s and activation cell %s is inconsistent", key.Cell(), act.Cell()), "internal/install/apply.DirectFileActivator.policyRequest", "before direct-file filesystem access", "no filesystem path was inspected or mutated", "construct the request from the matching activation binding and scoped key", nil)
	}
	policy, ok := a.policies[act.Cell()]
	if !ok {
		return DirectFilePolicy{}, request, cell.NewFault("direct-file request validation", "registered cell policy", fmt.Sprintf("cell %s has no direct-file policy", act.Cell()), "internal/install/apply.DirectFileActivator.policyRequest", "before direct-file filesystem access", "no filesystem path was inspected or mutated", "repair service policy construction", nil)
	}
	if err := policy.validate(request); err != nil {
		return DirectFilePolicy{}, request, cell.NewFault("direct-file policy validation", "cell policy accepts the typed request before filesystem access", err.Error(), "internal/install/apply.DirectFileActivator.policyRequest", "before generic direct-file inspection or mutation", "no filesystem path was inspected or mutated", "repair the cell-specific request or policy configuration and retry", err)
	}
	return policy, request, nil
}

func (a *DirectFileActivator) Inspect(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (Outcome, error) {
	policy, request, err := a.policyRequest(Inspect(), source, key, act, prior)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, genericErr := a.inspect(ctx, source, key, act, prior)
	decorated, decorateErr := policy.apply(request, out)
	if decorateErr != nil {
		return out, decorateErr
	}
	return decorated, genericErr
}

func (a *DirectFileActivator) inspect(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	tree, err := openSecureDirectTree(df.DestinationRoot(), false)
	if errors.Is(err, errSecureRootAbsent) {
		rec, recErr := a.record(source, key, df, prior, nil, registry.ObservationAbsent, false, registry.OperationInspect, registry.OutcomeCompleted, "")
		return Outcome{Status: Completed(), Observation: registry.ObservationAbsent, Record: &rec}, recErr
	}
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	defer tree.close()
	type liveLeaf struct {
		leaf           registry.Leaf
		current, prior bool
	}
	var live []liveLeaf
	present, absent := 0, 0
	priorByPath := map[string]registry.Leaf{}
	if prior != nil {
		for _, leaf := range prior.Leaves() {
			priorByPath[leaf.Path().String()] = leaf
		}
	}
	for _, entry := range df.Bundle().Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		identity, exists, identityErr := tree.identity(entry.Path().String())
		if identityErr != nil {
			return Outcome{Observation: registry.ObservationUnknown}, identityErr
		}
		if !exists {
			absent++
			continue
		}
		leaf, leafErr := registry.NewLeaf(entry.Path(), artifact.RegularFileType(), identity.mode, identity.digest)
		if leafErr != nil {
			return Outcome{Observation: registry.ObservationUnknown}, leafErr
		}
		priorLeaf, hasPrior := priorByPath[entry.Path().String()]
		live = append(live, liveLeaf{leaf: leaf, current: leaf.Digest() == entry.Digest() && leaf.Mode().Bits() == entry.Mode().Bits(), prior: hasPrior && sameLeaf(leaf, priorLeaf)})
		present++
	}
	if present > 0 && absent > 0 {
		return Outcome{Observation: registry.ObservationUnknown}, cell.NewFault("direct-file inspection", "complete installed or absent bundle", "only part of the bundle is present", df.DestinationRoot(), "classifying live direct-file state", "partial state cannot be adopted or removed safely", "restore the recorded leaves or move the partial external tree aside, then retry", nil)
	}
	if absent > 0 {
		rec, recErr := a.record(source, key, df, prior, nil, registry.ObservationAbsent, false, registry.OperationInspect, registry.OutcomeCompleted, "")
		return Outcome{Status: Completed(), Observation: registry.ObservationAbsent, Record: &rec}, recErr
	}
	allCurrent, allPrior := true, prior != nil && prior.Managed()
	leaves := make([]registry.Leaf, 0, len(live))
	for _, item := range live {
		allCurrent = allCurrent && item.current
		allPrior = allPrior && item.prior
		leaves = append(leaves, item.leaf)
	}
	if !allCurrent && !allPrior {
		return Outcome{Observation: registry.ObservationUnknown}, cell.NewFault("direct-file inspection", "exact current bundle or exact prior managed identity", "live files match neither the desired bundle nor the recorded ownership token", df.DestinationRoot(), "classifying live direct-file state", "overwriting or removing could discard user changes", "restore the recorded files or move the conflicting tree aside, then retry", nil)
	}
	managed := allPrior
	diagnostic := ""
	if allCurrent && !managed {
		diagnostic = "an exact external copy is present; it remains externally owned and will never be removed by Pasture"
	}
	rec, recErr := a.record(source, key, df, prior, leaves, registry.ObservationInstalled, managed, registry.OperationInspect, registry.OutcomeCompleted, diagnostic)
	return Outcome{Status: Completed(), Observation: registry.ObservationInstalled, Record: &rec, Diagnostic: diagnostic}, recErr
}

func (a *DirectFileActivator) Ensure(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (Outcome, error) {
	policy, request, err := a.policyRequest(Ensure(), source, key, act, prior)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, genericErr := a.ensure(ctx, source, key, act, prior)
	decorated, decorateErr := policy.apply(request, out)
	if decorateErr != nil {
		return out, decorateErr
	}
	return decorated, genericErr
}

func (a *DirectFileActivator) ensure(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	tree, err := openSecureDirectTree(df.DestinationRoot(), true)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	defer tree.close()
	priorByPath := map[string]registry.Leaf{}
	if prior != nil && prior.Managed() {
		for _, leaf := range prior.Leaves() {
			priorByPath[leaf.Path().String()] = leaf
		}
	}
	managed := false
	createdDirs := []artifact.Path(nil)
	for _, entry := range df.Bundle().Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		identity, exists, identityErr := tree.identity(entry.Path().String())
		if identityErr != nil {
			return a.inspectAfterFailure(ctx, source, key, act, prior, registry.OperationEnsure, identityErr)
		}
		priorLeaf, hasPrior := priorByPath[entry.Path().String()]
		if exists {
			matchesDesired := identity.digest == entry.Digest() && identity.mode.Bits() == entry.Mode().Bits()
			if hasPrior && !secureIdentityMatchesLeaf(identity, priorLeaf) {
				return a.inspectAfterFailure(ctx, source, key, act, prior, registry.OperationEnsure, directPathFault(entry.Path().String(), "the managed leaf drifted from its ownership token", "restore the recorded leaf or move it aside and retry"))
			}
			if matchesDesired {
				continue
			}
			if !hasPrior {
				return a.inspectAfterFailure(ctx, source, key, act, prior, registry.OperationEnsure, directPathFault(entry.Path().String(), "a different external leaf occupies the destination", "move the external leaf aside and retry"))
			}
		}
		content, readErr := readBundleLeaf(df.Bundle(), entry.Path().String())
		if readErr != nil {
			return a.inspectAfterFailure(ctx, source, key, act, prior, registry.OperationEnsure, readErr)
		}
		made, writeErr := tree.write(entry.Path().String(), content, entry.Mode().Bits())
		if writeErr != nil {
			return a.inspectAfterFailure(ctx, source, key, act, prior, registry.OperationEnsure, writeErr)
		}
		managed = true
		for _, rel := range made {
			p, parseErr := artifact.NewPath(rel)
			if parseErr != nil {
				return Outcome{Observation: registry.ObservationUnknown}, parseErr
			}
			createdDirs = appendUniquePath(createdDirs, p)
		}
	}
	out, inspectErr := a.inspect(ctx, source, key, act, prior)
	if inspectErr != nil {
		return out, cell.NewFault("direct-file ensure", "live-confirmed postcondition", inspectErr.Error(), df.DestinationRoot(), "inspecting after ensure", "the write may have succeeded but no unproved fact will be persisted", "inspect the destination and rerun", inspectErr)
	}
	managed = managed || prior != nil && prior.Managed()
	if managed {
		out.Diagnostic = ""
	}
	created := append([]artifact.Path(nil), out.Record.CreatedDirs()...)
	for _, dir := range createdDirs {
		created = appendUniquePath(created, dir)
	}
	rec, recErr := registry.NewRecord(registry.RecordInput{Key: key, Source: registrySource(source), Strategy: activation.DirectFileKindValue(), Managed: managed, ArtifactID: df.Bundle().ID(), Leaves: out.Record.Leaves(), CreatedDirs: created, Observation: out.Observation, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted, Diagnostic: out.Diagnostic})
	if recErr != nil {
		return Outcome{Observation: out.Observation}, recErr
	}
	return Outcome{Status: Completed(), Observation: out.Observation, Record: &rec, Diagnostic: out.Diagnostic}, nil
}

func (a *DirectFileActivator) Remove(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (Outcome, error) {
	policy, request, err := a.policyRequest(RemoveOp(), source, key, act, &prior)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, genericErr := a.remove(ctx, source, key, act, prior)
	decorated, decorateErr := policy.apply(request, out)
	if decorateErr != nil {
		return out, decorateErr
	}
	return decorated, genericErr
}

func (a *DirectFileActivator) remove(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	tree, err := openSecureDirectTree(df.DestinationRoot(), false)
	if errors.Is(err, errSecureRootAbsent) {
		rec, recErr := a.record(source, key, df, &prior, nil, registry.ObservationAbsent, true, registry.OperationRemove, registry.OutcomeCompleted, "")
		return Outcome{Status: Completed(), Observation: registry.ObservationAbsent, Record: &rec}, recErr
	}
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	defer tree.close()
	for _, leaf := range prior.Leaves() {
		identity, exists, identityErr := tree.identity(leaf.Path().String())
		if identityErr != nil {
			return a.inspectAfterFailure(ctx, source, key, act, &prior, registry.OperationRemove, identityErr)
		}
		if !exists {
			continue
		}
		if !secureIdentityMatchesLeaf(identity, leaf) {
			return a.inspectAfterFailure(ctx, source, key, act, &prior, registry.OperationRemove, directPathFault(leaf.Path().String(), "the managed leaf drifted from its ownership token", "restore the recorded leaf or move it aside and retry"))
		}
		if unlinkErr := tree.unlink(leaf.Path().String()); unlinkErr != nil {
			return a.inspectAfterFailure(ctx, source, key, act, &prior, registry.OperationRemove, unlinkErr)
		}
	}
	preserved := []string(nil)
	dirs := prior.CreatedDirs()
	for i := len(dirs) - 1; i >= 0; i-- {
		removed, removeErr := tree.removeDir(dirs[i].String())
		if removeErr != nil {
			return a.inspectAfterFailure(ctx, source, key, act, &prior, registry.OperationRemove, removeErr)
		}
		if !removed {
			preserved = append(preserved, dirs[i].String())
		}
	}
	out, inspectErr := a.inspect(ctx, source, key, act, &prior)
	if inspectErr != nil {
		return out, cell.NewFault("direct-file remove", "live-confirmed absent postcondition", inspectErr.Error(), df.DestinationRoot(), "inspecting after remove", "the unlink may have partially succeeded but no unproved fact will be persisted", "inspect the destination and rerun removal", inspectErr)
	}
	if out.Observation != registry.ObservationAbsent {
		postErr := cell.NewFault("direct-file remove", "live-confirmed absent postcondition", fmt.Sprintf("live observation is %s after removal", out.Observation), df.DestinationRoot(), "inspecting after remove", "the component remains installed or its state is unknown, so uninstall is failed and retryable", "preserve the remaining files, inspect their ownership, and rerun removal", nil)
		return a.inspectAfterFailure(ctx, source, key, act, &prior, registry.OperationRemove, postErr)
	}
	diagnostic := ""
	if len(preserved) > 0 {
		diagnostic = fmt.Sprintf("managed leaves are absent; created directories %v were preserved because they contain external entries", preserved)
	}
	rec, recErr := a.record(source, key, df, &prior, nil, registry.ObservationAbsent, true, registry.OperationRemove, registry.OutcomeCompleted, diagnostic)
	if recErr != nil {
		return Outcome{Observation: registry.ObservationAbsent}, recErr
	}
	return Outcome{Status: Completed(), Observation: registry.ObservationAbsent, Record: &rec, Diagnostic: diagnostic}, nil
}

func (a *DirectFileActivator) inspectAfterFailure(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record, op registry.Operation, actionErr error) (Outcome, error) {
	out, inspectErr := a.inspect(ctx, source, key, act, prior)
	diagnostic := actionErr.Error()
	if inspectErr != nil {
		diagnostic += "; secondary live inspection also failed: " + inspectErr.Error()
		out.Observation = registry.ObservationUnknown
	}
	df, _ := a.strategy(act)
	var leaves []registry.Leaf
	managed := prior != nil && prior.Managed()
	if out.Record != nil {
		leaves = out.Record.Leaves()
		// A successful live inspection is stronger than historical authority.
		// Preserve its exact ownership; only carry prior management when live
		// inspection could not establish a record at all.
		managed = out.Record.Managed()
	}
	rec, recErr := a.record(source, key, df, prior, leaves, out.Observation, managed, op, registry.OutcomeFailed, diagnostic)
	if recErr == nil {
		out.Record = &rec
	}
	if recErr != nil {
		diagnostic += "; failed to construct confirmed registry fact: " + recErr.Error()
	}
	return out, fmt.Errorf("%s", diagnostic)
}

func (a *DirectFileActivator) record(source Source, key registry.Key, df activation.DirectFile, prior *registry.Record, leaves []registry.Leaf, observation registry.Observation, managed bool, op registry.Operation, outcome registry.Outcome, diagnostic string) (registry.Record, error) {
	created := []artifact.Path(nil)
	if prior != nil {
		created = prior.CreatedDirs()
	}
	if observation == registry.ObservationAbsent {
		leaves = nil
		created = nil
	}
	return registry.NewRecord(registry.RecordInput{Key: key, Source: registrySource(source), Strategy: activation.DirectFileKindValue(), Managed: managed, ArtifactID: df.Bundle().ID(), Leaves: leaves, CreatedDirs: created, Observation: observation, Trust: registry.TrustNotApplicable, LastOperation: op, LastOutcome: outcome, Diagnostic: diagnostic})
}

func registrySource(source Source) registry.Source {
	if source == SourceHomeManager {
		return registry.SourceHomeManager
	}
	return registry.SourceInstaller
}
func sameLeaf(a, b registry.Leaf) bool {
	return a.Path() == b.Path() && a.Type() == b.Type() && a.Mode().Bits() == b.Mode().Bits() && a.Digest() == b.Digest()
}

func secureIdentityMatchesLeaf(identity secureIdentity, leaf registry.Leaf) bool {
	return leaf.Type() == artifact.RegularFileType() && identity.digest == leaf.Digest() && identity.mode.Bits() == leaf.Mode().Bits()
}

func readBundleLeaf(bundle artifact.Bundle, name string) ([]byte, error) {
	file, err := bundle.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func appendUniquePath(paths []artifact.Path, candidate artifact.Path) []artifact.Path {
	for _, existing := range paths {
		if existing == candidate {
			return paths
		}
	}
	return append(paths, candidate)
}
