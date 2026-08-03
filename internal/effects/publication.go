package effects

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// Publisher file and directory modes are a fixed publisher policy: an ir
// RenderedTree carries content but not mode, so publication writes regular
// files and directories with these exact modes and verifies them.
const (
	publishedFileMode fs.FileMode = 0o644
	publishedDirMode  fs.FileMode = 0o755
)

// manifestSuffix is appended to a hidden, same-parent sidecar that records the
// last confirmed published tree. The sidecar lives outside the payload root, so
// it is never part of payload content equality.
const manifestSuffix = ".pasture-manifest.json"

// NodeType is the closed set of filesystem node types publication distinguishes.
type NodeType string

const (
	// NodeAbsent means no node exists at the path.
	NodeAbsent NodeType = "absent"
	// NodeFile means a regular file exists at the path.
	NodeFile NodeType = "file"
	// NodeDir means a directory exists at the path.
	NodeDir NodeType = "dir"
	// NodeOther means an irregular node (symlink, device, ...) exists.
	NodeOther NodeType = "other"
)

// PublishedNode is the observed state of one filesystem path.
type PublishedNode struct {
	Type NodeType
	Mode fs.FileMode
}

// PublicationFS is the injected filesystem seam publication reconciles against.
// Production wires an os-backed implementation; tests wire an in-memory fake.
// The reconciliation policy lives entirely in the publisher, never in an
// implementation of this seam.
type PublicationFS interface {
	// Stat reports the node at an absolute-or-root-relative path. A missing path
	// returns NodeAbsent with a nil error.
	Stat(path string) (PublishedNode, error)
	// ReadFile reads the exact content of a regular file.
	ReadFile(path string) ([]byte, error)
	// WriteFile writes exact content to a regular file with mode, replacing any
	// prior regular-file content.
	WriteFile(path string, content []byte, mode fs.FileMode) error
	// MkdirAll ensures a directory (and parents) exists with mode.
	MkdirAll(path string, mode fs.FileMode) error
	// Remove removes exactly the node at path.
	Remove(path string) error
}

// PathOutcome is the closed set of per-path reconciliation results.
type PathOutcome string

const (
	// PathVerified means the on-disk state already matched the desired state.
	PathVerified PathOutcome = "verified"
	// PathCreated means a new desired file was written.
	PathCreated PathOutcome = "created"
	// PathUpdated means an existing managed file was rewritten to desired.
	PathUpdated PathOutcome = "updated"
	// PathRemoved means a stale managed leaf was removed.
	PathRemoved PathOutcome = "removed"
	// PathFailed means reconciliation of the path failed.
	PathFailed PathOutcome = "failed"
)

// PathResult is the exact per-path reconciliation result.
type PathResult struct {
	Path    string
	Outcome PathOutcome
	Err     error
}

// PublishReport is the exact result of a publication. Results holds one entry
// per reconciled or attempted path in deterministic path order. ManifestReplaced
// reports whether the sidecar was advanced to the new confirmed manifest; on any
// payload failure it stays false and the last confirmed manifest is retained.
type PublishReport struct {
	Results          []PathResult
	ManifestReplaced bool
}

// Failed reports whether any path result failed.
func (r PublishReport) Failed() bool {
	for _, result := range r.Results {
		if result.Outcome == PathFailed {
			return true
		}
	}
	return false
}

type manifestEntry struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Mode   uint32 `json:"mode"`
}

type manifestDocument struct {
	Entries []manifestEntry `json:"entries"`
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Publish reconciles a fully validated immutable RenderedTree into the
// publisher-owned payloadRoot and advances a hidden same-parent sidecar
// (.<payload-root>.pasture-manifest.json) only after the payload verifies. It
// creates, updates, and removes to reach exact final path/type/mode/content
// equality with the tree, including stale-leaf removal. It verifies payload
// first and replaces the sidecar last, reports exact per-path partial results,
// retains the last confirmed manifest on failure, and lets the same tree retry
// resume from old-or-desired matching states. It fails before any mutation on a
// sidecar collision or unsafe type, on unrelated drift, or when a partial prior
// publish is unreconciled and the desired tree has changed. It never claims
// rollback or an atomic directory swap.
//
// Publish must only ever be called with a RenderedTree from a successful
// compile+lower+native-load; a caller must not invoke it on any such failure.
func Publish(tree ir.RenderedTree, payloadRoot string, filesystem PublicationFS) (PublishReport, error) {
	if tree.Len() == 0 {
		return PublishReport{}, effectError(
			"publication tree is empty",
			"publication accepts only a fully validated immutable RenderedTree produced by a successful compile",
			"Publish", "publication preflight",
			"there is nothing to publish and the call is likely a compile-error path that must not publish",
			"pass a non-empty RenderedTree from a successful Compile, and never call Publish on a compile/lower/native-load error", nil,
		)
	}
	if filesystem == nil {
		return PublishReport{}, effectError(
			"publication filesystem is nil",
			"reconciliation requires an injected PublicationFS",
			"Publish", "publication preflight",
			"no reconciliation can be performed",
			"pass a non-nil PublicationFS (an os-backed one in production, a fake in tests)", nil,
		)
	}
	root, sidecar, err := derivePayloadPaths(payloadRoot)
	if err != nil {
		return PublishReport{}, err
	}

	desired, order, err := desiredState(tree, root)
	if err != nil {
		return PublishReport{}, err
	}
	if _, clash := desired[sidecar]; clash {
		return PublishReport{}, sidecarCollision(sidecar)
	}

	// Preflight the sidecar's type before trusting it as a manifest.
	sidecarNode, err := filesystem.Stat(sidecar)
	if err != nil {
		return PublishReport{}, statError(sidecar, err)
	}
	if sidecarNode.Type != NodeAbsent && sidecarNode.Type != NodeFile {
		return PublishReport{}, effectError(
			fmt.Sprintf("sidecar %q is not a regular file (found %s)", sidecar, sidecarNode.Type),
			"the manifest sidecar must be a regular file the publisher owns; an unsafe type could be a foreign directory or link",
			"Publish", "publication preflight",
			"publication refuses to overwrite an unowned sidecar node before any mutation",
			"remove or relocate the conflicting sidecar node, then retry", nil,
		)
	}

	previous, err := loadManifest(filesystem, sidecar, sidecarNode.Type, root)
	if err != nil {
		return PublishReport{}, err
	}

	// Preflight every managed path before mutating anything: reject unrelated
	// drift and a changed desired tree over an unreconciled partial publish.
	if err := preflightDrift(filesystem, desired, previous); err != nil {
		return PublishReport{}, err
	}

	// Payload reconciliation (payload first).
	report := PublishReport{}
	for _, relative := range order {
		full := path.Join(root, relative)
		desiredContent := desired[full]
		outcome, mutateErr := reconcileFile(filesystem, full, desiredContent)
		report.Results = append(report.Results, PathResult{Path: full, Outcome: outcome, Err: mutateErr})
		if mutateErr != nil {
			return report, publishFailure(full, mutateErr)
		}
	}

	// Stale-leaf removal: paths the last manifest owned that the tree no longer
	// desires.
	var staleFull []string
	for full := range previous {
		if _, keep := desired[full]; !keep {
			staleFull = append(staleFull, full)
		}
	}
	sort.Strings(staleFull)
	for _, full := range staleFull {
		outcome, removeErr := removeStaleLeaf(filesystem, full)
		report.Results = append(report.Results, PathResult{Path: full, Outcome: outcome, Err: removeErr})
		if removeErr != nil {
			return report, publishFailure(full, removeErr)
		}
	}

	// Verify final equality before touching the sidecar.
	if err := verifyFinalState(filesystem, desired, previous); err != nil {
		return report, err
	}

	// Replace the sidecar last.
	if err := writeManifest(filesystem, sidecar, desired, root); err != nil {
		return report, effectError(
			fmt.Sprintf("sidecar %q could not be written after payload verification", sidecar),
			"the manifest records the confirmed tree and is replaced only after the payload verifies",
			"Publish", "publication sidecar replacement",
			"the payload is published but the confirmed manifest was not advanced; a retry will re-verify and re-write it",
			"ensure the sidecar path is writable, then retry the same tree to advance the manifest", err,
		)
	}
	report.ManifestReplaced = true
	return report, nil
}

func derivePayloadPaths(payloadRoot string) (root, sidecar string, err error) {
	if payloadRoot == "" || strings.TrimSpace(payloadRoot) != payloadRoot {
		return "", "", effectError(
			"payload root is empty or padded",
			"publication needs one exact owned payload root directory",
			"Publish", "publication preflight",
			"the payload has no deterministic root",
			"supply a non-empty payload root path without surrounding whitespace", nil,
		)
	}
	cleaned := path.Clean(payloadRoot)
	base := path.Base(cleaned)
	if base == "." || base == "/" || base == ".." {
		return "", "", effectError(
			fmt.Sprintf("payload root %q does not name a specific directory", payloadRoot),
			"the sidecar is derived from the payload root's own name, so the root must be a specific directory",
			"Publish", "publication preflight",
			"a sidecar name cannot be derived",
			"supply a payload root that names a specific directory", nil,
		)
	}
	parent := path.Dir(cleaned)
	sidecar = path.Join(parent, "."+base+manifestSuffix)
	return cleaned, sidecar, nil
}

func desiredState(tree ir.RenderedTree, root string) (map[string][]byte, []string, error) {
	desired := make(map[string][]byte)
	order := tree.Paths() // ir.RenderedTree.Paths returns sorted paths
	for _, relative := range order {
		file, ok := tree.File(relative)
		if !ok {
			return nil, nil, effectError(
				fmt.Sprintf("rendered tree path %q disappeared during read", relative),
				"a validated RenderedTree must return every path it enumerates",
				"Publish", "publication preflight",
				"the desired state is inconsistent",
				"reconstruct the RenderedTree from a fresh Compile", nil,
			)
		}
		full := path.Join(root, relative)
		desired[full] = file.Content()
	}
	return desired, order, nil
}

func sidecarCollision(sidecar string) error {
	return effectError(
		fmt.Sprintf("sidecar %q collides with a payload path", sidecar),
		"the manifest sidecar is namespace-separated from payload content and must never equal a published path",
		"Publish", "publication preflight",
		"publication refuses to conflate the manifest with payload content before any mutation",
		"choose a payload root whose derived sidecar does not equal a payload path", nil,
	)
}

func statError(target string, cause error) error {
	return effectError(
		fmt.Sprintf("path %q could not be inspected", target),
		"reconciliation must know each path's current state before deciding to mutate it",
		"Publish", "publication preflight",
		"publication cannot safely proceed without the current state",
		"ensure the path is readable, then retry", cause,
	)
}

func loadManifest(filesystem PublicationFS, sidecar string, sidecarType NodeType, root string) (map[string][]byte, error) {
	if sidecarType != NodeFile {
		return map[string][]byte{}, nil
	}
	raw, err := filesystem.ReadFile(sidecar)
	if err != nil {
		return nil, effectError(
			fmt.Sprintf("sidecar manifest %q could not be read", sidecar),
			"the last confirmed manifest defines the previously owned tree used for stale-leaf removal and drift detection",
			"Publish", "publication preflight",
			"publication cannot determine which leaves it previously owned",
			"ensure the sidecar is readable, or remove it to publish from a clean slate", err,
		)
	}
	var document manifestDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, effectError(
			fmt.Sprintf("sidecar manifest %q is malformed", sidecar),
			"a manifest must be exact JSON so drift and ownership decisions are trustworthy",
			"Publish", "publication preflight",
			"publication cannot trust the recorded ownership",
			"restore or remove the corrupted sidecar, then retry", err,
		)
	}
	previous := make(map[string][]byte, len(document.Entries))
	for _, entry := range document.Entries {
		previous[path.Join(root, entry.Path)] = []byte(entry.Digest)
	}
	return previous, nil
}

// preflightDrift rejects, before any mutation, a managed path whose current
// content matches neither the desired tree nor the last confirmed manifest
// (unrelated drift), and a payload path present as a non-file node.
func preflightDrift(filesystem PublicationFS, desired map[string][]byte, previous map[string][]byte) error {
	managed := make(map[string]struct{}, len(desired)+len(previous))
	for full := range desired {
		managed[full] = struct{}{}
	}
	for full := range previous {
		managed[full] = struct{}{}
	}
	ordered := make([]string, 0, len(managed))
	for full := range managed {
		ordered = append(ordered, full)
	}
	sort.Strings(ordered)
	for _, full := range ordered {
		node, err := filesystem.Stat(full)
		if err != nil {
			return statError(full, err)
		}
		if node.Type == NodeAbsent {
			continue
		}
		if node.Type != NodeFile {
			return effectError(
				fmt.Sprintf("managed path %q is a %s, not a regular file", full, node.Type),
				"a managed payload path must be a regular file the publisher owns; an unexpected node type is unrelated drift",
				"Publish", "publication drift preflight",
				"publication refuses to remove or overwrite an unowned node before any mutation",
				"remove or relocate the conflicting node, then retry", nil,
			)
		}
		content, err := filesystem.ReadFile(full)
		if err != nil {
			return statError(full, err)
		}
		currentDigest := digestOf(content)
		if desiredContent, isDesired := desired[full]; isDesired {
			if currentDigest == digestOf(desiredContent) {
				continue // already at desired
			}
		}
		if previousDigest, wasOwned := previous[full]; wasOwned {
			if currentDigest == string(previousDigest) {
				continue // matches the last confirmed manifest
			}
		}
		return effectError(
			fmt.Sprintf("managed path %q holds unrelated content", full),
			"the path matches neither the desired tree nor the last confirmed manifest, so a prior partial publish or foreign edit is unreconciled and the desired tree may have changed underneath it",
			"Publish", "publication drift preflight",
			"publication refuses to overwrite unrelated content before any mutation and makes no rollback claim",
			"restore the path to the last confirmed content or the desired content, then retry", nil,
		)
	}
	return nil
}

func reconcileFile(filesystem PublicationFS, full string, desiredContent []byte) (PathOutcome, error) {
	node, err := filesystem.Stat(full)
	if err != nil {
		return PathFailed, err
	}
	if node.Type == NodeFile {
		current, readErr := filesystem.ReadFile(full)
		if readErr != nil {
			return PathFailed, readErr
		}
		if digestOf(current) == digestOf(desiredContent) && node.Mode.Perm() == publishedFileMode {
			return PathVerified, nil
		}
	}
	if parent := path.Dir(full); parent != "." && parent != "/" {
		if err := filesystem.MkdirAll(parent, publishedDirMode); err != nil {
			return PathFailed, err
		}
	}
	if err := filesystem.WriteFile(full, desiredContent, publishedFileMode); err != nil {
		return PathFailed, err
	}
	if node.Type == NodeFile {
		return PathUpdated, nil
	}
	return PathCreated, nil
}

func removeStaleLeaf(filesystem PublicationFS, full string) (PathOutcome, error) {
	node, err := filesystem.Stat(full)
	if err != nil {
		return PathFailed, err
	}
	if node.Type == NodeAbsent {
		return PathRemoved, nil // already gone; resume converges
	}
	if err := filesystem.Remove(full); err != nil {
		return PathFailed, err
	}
	return PathRemoved, nil
}

func verifyFinalState(filesystem PublicationFS, desired map[string][]byte, previous map[string][]byte) error {
	for full, content := range desired {
		node, err := filesystem.Stat(full)
		if err != nil {
			return err
		}
		if node.Type != NodeFile || node.Mode.Perm() != publishedFileMode {
			return effectError(
				fmt.Sprintf("published path %q did not reach the desired type/mode", full),
				"publication verifies exact final path/type/mode/content equality with the RenderedTree",
				"Publish", "publication verification",
				"the payload is not confirmed and the manifest is not advanced",
				"retry the same tree to reconcile the path", nil,
			)
		}
		actual, err := filesystem.ReadFile(full)
		if err != nil {
			return err
		}
		if digestOf(actual) != digestOf(content) {
			return effectError(
				fmt.Sprintf("published path %q did not reach the desired content", full),
				"publication verifies exact final content equality with the RenderedTree",
				"Publish", "publication verification",
				"the payload is not confirmed and the manifest is not advanced",
				"retry the same tree to reconcile the path", nil,
			)
		}
	}
	for full := range previous {
		if _, keep := desired[full]; keep {
			continue
		}
		node, err := filesystem.Stat(full)
		if err != nil {
			return err
		}
		if node.Type != NodeAbsent {
			return effectError(
				fmt.Sprintf("stale path %q was not removed", full),
				"publication verifies stale leaves are gone before advancing the manifest",
				"Publish", "publication verification",
				"the payload still holds a stale leaf and the manifest is not advanced",
				"retry the same tree to complete stale-leaf removal", nil,
			)
		}
	}
	return nil
}

func writeManifest(filesystem PublicationFS, sidecar string, desired map[string][]byte, root string) error {
	entries := make([]manifestEntry, 0, len(desired))
	for full, content := range desired {
		relative := strings.TrimPrefix(full, root+"/")
		entries = append(entries, manifestEntry{
			Path:   relative,
			Digest: digestOf(content),
			Mode:   uint32(publishedFileMode),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	encoded, err := json.Marshal(manifestDocument{Entries: entries})
	if err != nil {
		return err
	}
	return filesystem.WriteFile(sidecar, encoded, publishedFileMode)
}

func publishFailure(full string, cause error) error {
	return effectError(
		fmt.Sprintf("publication failed reconciling path %q", full),
		"a reconciliation step failed part-way through the payload",
		"Publish", "publication reconciliation",
		"the sidecar is not advanced, the last confirmed manifest is retained, and no rollback is attempted; the exact per-path results are reported",
		"resolve the underlying filesystem error and retry the same tree to resume", cause,
	)
}
