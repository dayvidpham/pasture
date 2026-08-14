package apply

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/directfile"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// DirectFileActivator is the real filesystem implementation for direct-file
// activation. Source controls registry ownership only; paths come exclusively
// from validated activation contracts.
type DirectFileActivator struct{}

func NewDirectFileActivator() DirectFileActivator { return DirectFileActivator{} }
func (DirectFileActivator) StrategyKind() activation.StrategyKind {
	return activation.DirectFileKindValue()
}

func (a DirectFileActivator) strategy(act activation.ComponentActivation) (activation.DirectFile, error) {
	df, ok := act.Strategy().(activation.DirectFile)
	if !ok || !df.IsValid() {
		return activation.DirectFile{}, cell.NewFault("direct-file activation", "valid direct-file strategy", fmt.Sprintf("cell %s is not bound to a valid direct-file strategy", act.Cell()), "internal/install/apply.DirectFileActivator.strategy", "dispatching a direct-file activation", "the wrong controller would inspect or mutate the cell", "bind this cell to a validated direct-file strategy", nil)
	}
	return df, nil
}

// Inspect is read-only and classifies exact current-bundle, exact prior-managed,
// wholly absent, and ambiguous/partial state without creating directories.
func (a DirectFileActivator) Inspect(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
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
		if err := checkParents(df.DestinationRoot(), entry.Path().String()); err != nil {
			return Outcome{Observation: registry.ObservationUnknown}, err
		}
		dest := filepath.Join(df.DestinationRoot(), filepath.FromSlash(entry.Path().String()))
		info, statErr := os.Lstat(dest)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				absent++
				continue
			}
			return Outcome{Observation: registry.ObservationUnknown}, cell.NewFault("direct-file inspection", "inspectable destination", statErr.Error(), dest, "reading live leaf metadata", "the service cannot establish ownership before an action", "repair path permissions and retry", statErr)
		}
		if info.Mode().Type()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return Outcome{Observation: registry.ObservationUnknown}, cell.NewFault("direct-file inspection", "regular non-symlink leaf", fmt.Sprintf("destination has unsafe type %s", info.Mode().Type()), dest, "classifying live direct-file state", "Pasture will preserve the entry because ownership is unproved", "move the conflicting entry aside and retry", nil)
		}
		file, openErr := os.Open(dest)
		if openErr != nil {
			return Outcome{Observation: registry.ObservationUnknown}, openErr
		}
		bytes, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			if readErr == nil {
				readErr = closeErr
			}
			return Outcome{Observation: registry.ObservationUnknown}, readErr
		}
		mode, modeErr := artifact.NewMode(uint32(info.Mode().Perm()))
		if modeErr != nil {
			return Outcome{Observation: registry.ObservationUnknown}, modeErr
		}
		leaf, leafErr := registry.NewLeaf(entry.Path(), artifact.RegularFileType(), mode, artifact.DigestBytes(bytes))
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

func (a DirectFileActivator) Ensure(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	var priorLeaves []registry.Leaf
	if prior != nil && prior.Managed() {
		priorLeaves = prior.Leaves()
	}
	ensureOut, ensureErr := directfile.Ensure(df.DestinationRoot(), df.Bundle(), priorLeaves)
	if ensureErr != nil {
		return a.inspectAfterFailure(ctx, source, key, act, prior, registry.OperationEnsure, ensureErr)
	}
	out, inspectErr := a.Inspect(ctx, source, key, act, prior)
	if inspectErr != nil {
		return out, cell.NewFault("direct-file ensure", "live-confirmed postcondition", inspectErr.Error(), df.DestinationRoot(), "inspecting after ensure", "the write may have succeeded but no unproved fact will be persisted", "inspect the destination and rerun", inspectErr)
	}
	managed := ensureOut.Managed || prior != nil && prior.Managed()
	if ensureOut.Managed {
		out.Diagnostic = ""
	}
	created := append([]artifact.Path(nil), out.Record.CreatedDirs()...)
	for _, dir := range ensureOut.CreatedDirs {
		p, parseErr := artifact.NewPath(dir)
		if parseErr != nil {
			return Outcome{Observation: out.Observation}, parseErr
		}
		created = append(created, p)
	}
	rec, recErr := registry.NewRecord(registry.RecordInput{Key: key, Source: registrySource(source), Strategy: activation.DirectFileKindValue(), Managed: managed, ArtifactID: df.Bundle().ID(), Leaves: out.Record.Leaves(), CreatedDirs: created, Observation: out.Observation, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted, Diagnostic: out.Diagnostic})
	if recErr != nil {
		return Outcome{Observation: out.Observation}, recErr
	}
	return Outcome{Status: Completed(), Observation: out.Observation, Record: &rec, Diagnostic: out.Diagnostic}, nil
}

func (a DirectFileActivator) Remove(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: registry.ObservationUnknown}, err
	}
	dirs := make([]string, 0, len(prior.CreatedDirs()))
	for _, d := range prior.CreatedDirs() {
		dirs = append(dirs, d.String())
	}
	removeOut, err := directfile.Remove(df.DestinationRoot(), prior.Leaves(), dirs)
	if err != nil {
		return a.inspectAfterFailure(ctx, source, key, act, &prior, registry.OperationRemove, err)
	}
	out, inspectErr := a.Inspect(ctx, source, key, act, &prior)
	if inspectErr != nil {
		return out, cell.NewFault("direct-file remove", "live-confirmed absent postcondition", inspectErr.Error(), df.DestinationRoot(), "inspecting after remove", "the unlink may have partially succeeded but no unproved fact will be persisted", "inspect the destination and rerun removal", inspectErr)
	}
	diagnostic := ""
	if len(removeOut.PreservedDirs) > 0 {
		diagnostic = fmt.Sprintf("managed leaves are absent; created directories %v were preserved because they contain external entries", removeOut.PreservedDirs)
	}
	rec, recErr := a.record(source, key, df, &prior, nil, registry.ObservationAbsent, true, registry.OperationRemove, registry.OutcomeCompleted, diagnostic)
	if recErr != nil {
		return Outcome{Observation: registry.ObservationAbsent}, recErr
	}
	return Outcome{Status: Completed(), Observation: registry.ObservationAbsent, Record: &rec, Diagnostic: diagnostic}, nil
}

func (a DirectFileActivator) inspectAfterFailure(ctx context.Context, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record, op registry.Operation, actionErr error) (Outcome, error) {
	out, inspectErr := a.Inspect(ctx, source, key, act, prior)
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
		managed = managed || out.Record.Managed()
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

func (a DirectFileActivator) record(source Source, key registry.Key, df activation.DirectFile, prior *registry.Record, leaves []registry.Leaf, observation registry.Observation, managed bool, op registry.Operation, outcome registry.Outcome, diagnostic string) (registry.Record, error) {
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

func checkParents(root, rel string) error {
	cleanRoot := filepath.Clean(root)
	if !filepath.IsAbs(cleanRoot) {
		return cell.NewFault("direct-file inspection", "absolute destination root", fmt.Sprintf("root %q is relative", root), root, "resolving a live destination", "working-directory changes could inspect the wrong path", "configure an absolute destination root", nil)
	}
	cur := cleanRoot
	parts := strings.Split(filepath.FromSlash(rel), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode().Type()&fs.ModeSymlink != 0 || !info.IsDir() {
			return cell.NewFault("direct-file inspection", "non-symlink directory boundary", fmt.Sprintf("unsafe parent type %s", info.Mode().Type()), cur, "walking destination parents", "inspection could escape or address the wrong tree", "replace the boundary with a real directory", nil)
		}
	}
	return nil
}
