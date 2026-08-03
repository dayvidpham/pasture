// Package directfile materializes and removes a published artifact bundle under
// a caller-supplied destination root using exact (type,digest,mode) ownership.
//
// Ensure creates absent leaves, upgrades leaves whose live identity still
// matches their recorded ownership token, treats an identical unrecorded leaf as
// external (satisfying desired state without adopting it), and rejects any
// differing, wrong-type, modified, symlinked, or traversing leaf without
// overwriting it. Remove unlinks only recorded managed leaves whose live
// identity still exactly matches, and removes only recorded created directories
// that are empty, preserving all siblings.
//
// Replacement uses one create-exclusive same-parent temp regular file per leaf,
// then write/flush/close/atomic sibling rename. The current process best-effort
// removes only the exact temp it created after a failure. A later process never
// scans, cleans, reuses, or infers ownership from crash orphans.
//
// Every directory level Pasture creates (not only the deepest) is recorded as a
// path relative to the destination root, so a later Remove can reclaim the whole
// tree it made instead of orphaning intermediate directories, and so the record
// survives a process restart independent of the absolute root.
//
// Threat model (accepted residual): each leaf is Lstat-checked and rejected if
// it is a symlink before Pasture reads or mutates it, but Go exposes no portable
// open-if-not-symlink primitive, so a local attacker with write access to the
// same directory could swap a regular file for a symlink in the narrow window
// between that Lstat and the subsequent open inside identify. The window cannot
// escalate to a write outside root: writeLeaf renames onto the leaf path (rename
// never follows a link) and Remove unlinks the directory entry itself, not a
// link target, so the worst case is a spurious ownership mismatch that refuses
// to touch the leaf. In the single-user deployment model this package targets,
// an attacker with that write access already holds the user's own rights to the
// directory.
package directfile

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/inventory"
)

// EnsureOutcome reports the confirmed leaves and how they got there.
type EnsureOutcome struct {
	// Leaves are the bundle's regular-file leaves now confirmed present with
	// their exact bundle identity, suitable for a managed inventory record.
	Leaves []inventory.Leaf
	// CreatedDirs are the directories Pasture created, each a clean path relative
	// to root, shallowest-first, including every intermediate level (not only the
	// deepest). Persist these in the inventory record so a later Remove can
	// reclaim the tree it made without orphaning intermediate directories.
	CreatedDirs []string
	// Managed is true when Pasture created or updated at least one leaf.
	Managed bool
	// External is true when every leaf already matched the bundle and Pasture
	// created nothing (a pre-existing external match).
	External bool
}

// Ensure materializes bundle under root. prior is the previously-recorded
// managed leaf set for this cell (empty on first install); it is the ownership
// token that authorizes update or removal.
func Ensure(root string, bundle artifact.Bundle, prior []inventory.Leaf) (EnsureOutcome, error) {
	priorByPath := indexLeaves(prior)
	entries := bundle.Manifest().Entries()

	var createdDirs []string
	created := false
	anyExternal := false
	allExternalOrPresent := true
	leaves := make([]inventory.Leaf, 0, len(entries))

	// Directories first (manifest entries are canonical-path sorted).
	for _, e := range entries {
		if !e.IsDirectory() {
			continue
		}
		made, err := ensureDirTree(root, e.Path().String(), e.Mode().Bits())
		if err != nil {
			return EnsureOutcome{}, err
		}
		if len(made) > 0 {
			created = true
			allExternalOrPresent = false
			createdDirs = append(createdDirs, made...)
		}
	}

	for _, e := range entries {
		if !e.IsRegular() {
			continue
		}
		dest, err := safeJoin(root, e.Path().String())
		if err != nil {
			return EnsureOutcome{}, err
		}
		// Ensure the parent directory (and any missing ancestor) exists,
		// recording every level created so Remove can reclaim the whole tree.
		if relParent := path.Dir(e.Path().String()); relParent != "." {
			made, err := ensureDirTree(root, relParent, 0o755)
			if err != nil {
				return EnsureOutcome{}, err
			}
			if len(made) > 0 {
				created = true
				allExternalOrPresent = false
				createdDirs = append(createdDirs, made...)
			}
		}

		content, err := readBundleFile(bundle, e.Path().String())
		if err != nil {
			return EnsureOutcome{}, err
		}
		leaf, err := inventory.NewLeaf(e.Path(), e.Type(), e.Mode(), e.Digest())
		if err != nil {
			return EnsureOutcome{}, err
		}

		info, statErr := os.Lstat(dest)
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				return EnsureOutcome{}, cell.NewFault(
					"direct-file ensure", "inspectable destination leaf",
					fmt.Sprintf("the destination leaf could not be inspected: %v", statErr),
					dest, "checking a leaf before writing",
					"the leaf cannot be safely created or verified",
					"ensure the destination directory is accessible, then retry", statErr,
				)
			}
			// Absent: create it.
			if err := writeLeaf(dest, content, e.Mode().Bits()); err != nil {
				return EnsureOutcome{}, err
			}
			created = true
			allExternalOrPresent = false
			leaves = append(leaves, leaf)
			continue
		}
		if info.Mode().Type()&fs.ModeSymlink != 0 {
			return EnsureOutcome{}, rejectLeaf(dest, "a symlink occupies the leaf path",
				"following or replacing the link could redirect a Pasture write outside the destination")
		}
		if !info.Mode().IsRegular() {
			return EnsureOutcome{}, rejectLeaf(dest, fmt.Sprintf("a %s occupies the leaf path", info.Mode().Type()),
				"Pasture will not overwrite a non-regular file")
		}
		liveIdentity, err := identify(dest)
		if err != nil {
			return EnsureOutcome{}, err
		}
		priorLeaf, hasPrior := priorByPath[e.Path().String()]
		switch {
		case hasPrior:
			if !identityMatchesLeaf(liveIdentity, priorLeaf) {
				return EnsureOutcome{}, rejectLeaf(dest,
					"the managed leaf was modified since Pasture last recorded it",
					"updating a drifted managed leaf could discard local changes")
			}
			if identityMatchesEntry(liveIdentity, e) {
				// Already current managed leaf; nothing to do.
				leaves = append(leaves, leaf)
				continue
			}
			// Managed upgrade: live matches prior record, so rewrite to bundle.
			if err := writeLeaf(dest, content, e.Mode().Bits()); err != nil {
				return EnsureOutcome{}, err
			}
			created = true
			allExternalOrPresent = false
			leaves = append(leaves, leaf)
		default:
			// No prior record: a foreign leaf.
			if identityMatchesEntry(liveIdentity, e) {
				// Exact external match: satisfy desired state, never adopt.
				anyExternal = true
				leaves = append(leaves, leaf)
				continue
			}
			return EnsureOutcome{}, rejectLeaf(dest,
				"an unrecorded leaf with different content or mode occupies the path",
				"Pasture will not overwrite a foreign file it did not create")
		}
	}

	return EnsureOutcome{
		Leaves:      leaves,
		CreatedDirs: createdDirs,
		Managed:     created,
		External:    !created && anyExternal && allExternalOrPresent,
	}, nil
}

// RemoveOutcome reports the result of an uninstall that could not fully reclaim
// its tree.
type RemoveOutcome struct {
	// PreservedDirs are recorded created directories, each relative to root, that
	// Remove intentionally left in place because they still hold entries Pasture
	// did not record (a foreign file dropped in later, or a surviving sibling).
	// They are reported, shallowest-first, so the caller can surface an
	// actionable note rather than deleting a directory it does not exclusively
	// own. An empty slice means the created tree was fully reclaimed.
	PreservedDirs []string
}

// Remove unlinks recorded managed leaves whose live identity still exactly
// matches, then removes recorded created directories that are empty, deepest
// first. A directory that still holds unrecorded entries is preserved and
// reported, never force-removed. Siblings are preserved. A leaf that drifted is
// left in place and the removal is rejected.
func Remove(root string, recorded []inventory.Leaf, createdDirs []string) (RemoveOutcome, error) {
	for _, l := range recorded {
		dest, err := safeJoin(root, l.Path().String())
		if err != nil {
			return RemoveOutcome{}, err
		}
		info, statErr := os.Lstat(dest)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue // already gone
			}
			return RemoveOutcome{}, cell.NewFault(
				"direct-file remove", "inspectable managed leaf",
				fmt.Sprintf("the managed leaf could not be inspected: %v", statErr),
				dest, "checking a managed leaf before unlink",
				"the leaf cannot be safely removed",
				"ensure the destination directory is accessible, then retry", statErr,
			)
		}
		if info.Mode().Type()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return RemoveOutcome{}, rejectLeaf(dest, "the recorded managed leaf is no longer a regular file",
				"Pasture will not unlink a path type it did not record")
		}
		liveIdentity, err := identify(dest)
		if err != nil {
			return RemoveOutcome{}, err
		}
		if !identityMatchesLeaf(liveIdentity, l) {
			return RemoveOutcome{}, rejectLeaf(dest, "the managed leaf drifted from its recorded ownership token",
				"removing a drifted leaf could discard local changes")
		}
		if err := os.Remove(dest); err != nil {
			return RemoveOutcome{}, cell.NewFault(
				"direct-file remove", "managed leaf unlinked",
				fmt.Sprintf("the managed leaf could not be unlinked: %v", err),
				dest, "unlinking a managed leaf",
				"the leaf remains installed",
				"ensure the destination directory is writable, then retry", err,
			)
		}
	}
	// Reclaim recorded created directories that are now empty, deepest first.
	// A directory recorded shallowest-first always precedes its descendants, so
	// reverse iteration removes a child before its parent and lets the emptiness
	// check cascade up the tree. Only recorded, now-empty directories are ever
	// removed; anything still holding an unrecorded entry is preserved.
	var preserved []string
	for i := len(createdDirs) - 1; i >= 0; i-- {
		rel := createdDirs[i]
		dir, err := safeJoin(root, rel)
		if err != nil {
			return RemoveOutcome{}, err
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue // already gone
			}
			// Unreadable: leave it in place and report rather than fail the
			// whole uninstall over a directory Pasture cannot inspect.
			preserved = append(preserved, rel)
			continue
		}
		if len(entries) != 0 {
			preserved = append(preserved, rel)
			continue
		}
		if err := os.Remove(dir); err != nil {
			preserved = append(preserved, rel)
		}
	}
	// Report shallowest-first for a stable, human-readable note.
	for l, r := 0, len(preserved)-1; l < r; l, r = l+1, r-1 {
		preserved[l], preserved[r] = preserved[r], preserved[l]
	}
	return RemoveOutcome{PreservedDirs: preserved}, nil
}

// ensureDirTree ensures rel (a clean relative directory path under root) and
// every missing ancestor exists, returning the relative paths of the
// directories it actually created, ordered shallowest-first. Recording every
// level it creates — not just the deepest — is what lets Remove later reclaim
// the whole tree instead of orphaning intermediate directories.
func ensureDirTree(root, rel string, mode uint32) ([]string, error) {
	var created []string
	cur := ""
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		if cur == "" {
			cur = part
		} else {
			cur += "/" + part
		}
		dest, err := safeJoin(root, cur)
		if err != nil {
			return nil, err
		}
		made, err := ensureDir(dest, mode)
		if err != nil {
			return nil, err
		}
		if made {
			created = append(created, cur)
		}
	}
	return created, nil
}

type identity struct {
	digest artifact.Digest
	mode   artifact.Mode
}

// identify opens dest once and derives both the content digest and the mode
// from that single handle. Deriving both from one fd (instead of an independent
// os.ReadFile plus os.Stat, each of which resolves the path separately) means a
// concurrent path swap between the two operations cannot make the digest and the
// mode describe two different inodes. The residual Lstat-then-open race the
// caller cannot close on Go's portable API is analyzed in the package doc; it
// cannot escalate past a spurious ownership mismatch.
func identify(dest string) (identity, error) {
	file, err := os.Open(dest)
	if err != nil {
		return identity{}, cell.NewFault(
			"direct-file identify", "readable leaf",
			fmt.Sprintf("the leaf could not be opened to compute its identity: %v", err),
			dest, "opening a leaf for ownership comparison",
			"ownership cannot be verified before mutation",
			"ensure the leaf is readable, then retry", err,
		)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return identity{}, cell.NewFault(
			"direct-file identify", "stattable leaf",
			fmt.Sprintf("the leaf mode could not be read: %v", err),
			dest, "reading a leaf mode for ownership comparison",
			"ownership cannot be verified before mutation",
			"ensure the leaf is accessible, then retry", err,
		)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return identity{}, cell.NewFault(
			"direct-file identify", "readable leaf",
			fmt.Sprintf("the leaf could not be read to compute its identity: %v", err),
			dest, "hashing a leaf for ownership comparison",
			"ownership cannot be verified before mutation",
			"ensure the leaf is readable, then retry", err,
		)
	}
	mode, err := artifact.NewMode(uint32(info.Mode().Perm()))
	if err != nil {
		return identity{}, err
	}
	return identity{digest: artifact.DigestBytes(content), mode: mode}, nil
}

func identityMatchesLeaf(id identity, leaf inventory.Leaf) bool {
	return leaf.Type() == artifact.RegularFileType() &&
		id.digest == leaf.Digest() && id.mode.Bits() == leaf.Mode().Bits()
}

func identityMatchesEntry(id identity, e artifact.Entry) bool {
	return e.IsRegular() && id.digest == e.Digest() && id.mode.Bits() == e.Mode().Bits()
}

func indexLeaves(leaves []inventory.Leaf) map[string]inventory.Leaf {
	out := make(map[string]inventory.Leaf, len(leaves))
	for _, l := range leaves {
		out[l.Path().String()] = l
	}
	return out
}

// safeJoin rejects any path that would escape root. artifact.Path is already
// clean and relative, but the traversal guard is defense-in-depth.
func safeJoin(root, rel string) (string, error) {
	joined := filepath.Join(root, rel)
	cleanRoot := filepath.Clean(root)
	if joined != cleanRoot && !hasPrefixDir(joined, cleanRoot) {
		return "", cell.NewFault(
			"direct-file path resolution", "leaf stays under the destination root",
			fmt.Sprintf("the leaf path %q resolves outside the root %q", rel, root),
			joined, "resolving a leaf destination",
			"a traversing path could write outside the intended harness root",
			"provide a clean relative bundle path", nil,
		)
	}
	return joined, nil
}

func hasPrefixDir(path, root string) bool {
	if len(path) <= len(root) {
		return false
	}
	return path[:len(root)] == root && path[len(root)] == filepath.Separator
}

func ensureDir(path string, mode uint32) (bool, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode().Type()&fs.ModeSymlink != 0 {
			return false, rejectLeaf(path, "a symlink occupies the directory path",
				"following the link could redirect writes outside the destination")
		}
		if !info.IsDir() {
			return false, rejectLeaf(path, "a non-directory occupies the directory path",
				"Pasture will not replace a file with a directory")
		}
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, cell.NewFault(
			"direct-file ensure", "inspectable directory",
			fmt.Sprintf("the directory could not be inspected: %v", err),
			path, "checking a directory before creation",
			"the directory cannot be safely created",
			"ensure the parent directory is accessible, then retry", err,
		)
	}
	if err := os.MkdirAll(path, os.FileMode(mode)); err != nil {
		return false, cell.NewFault(
			"direct-file ensure", "created directory",
			fmt.Sprintf("the directory could not be created: %v", err),
			path, "creating a bundle directory",
			"the bundle leaves cannot be materialized",
			"ensure the parent directory is writable, then retry", err,
		)
	}
	return true, nil
}

func writeLeaf(dest string, content []byte, mode uint32) error {
	dir := filepath.Dir(dest)
	tempPath, err := freshTempPath(dir)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(tempPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, os.FileMode(mode))
	if err != nil {
		return cell.NewFault(
			"direct-file write", "create-exclusive temp leaf",
			fmt.Sprintf("the temp leaf could not be created exclusively: %v", err),
			tempPath, "allocating a temp leaf",
			"the leaf write cannot begin and nothing was modified",
			"ensure the destination directory is writable, then retry", err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return cell.NewFault(
			"direct-file write", "temp bytes flushed",
			fmt.Sprintf("writing the leaf bytes failed: %v", err),
			tempPath, "flushing leaf bytes before rename",
			"the destination leaf was not modified and the temp is being removed",
			"ensure the directory has space and is writable, then retry", err,
		)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return cell.NewFault(
			"direct-file write", "temp durable",
			fmt.Sprintf("syncing the leaf failed: %v", err),
			tempPath, "flushing leaf bytes to stable storage",
			"the destination leaf was not modified and the temp is being removed",
			"ensure the filesystem supports fsync, then retry", err,
		)
	}
	// Enforce the exact mode regardless of umask applied at create time.
	if err := file.Chmod(os.FileMode(mode)); err != nil {
		_ = file.Close()
		return cell.NewFault(
			"direct-file write", "exact leaf mode",
			fmt.Sprintf("the leaf mode could not be set: %v", err),
			tempPath, "setting the exact leaf mode",
			"the destination leaf was not modified and the temp is being removed",
			"ensure the filesystem supports chmod, then retry", err,
		)
	}
	if err := file.Close(); err != nil {
		return cell.NewFault(
			"direct-file write", "temp closed",
			fmt.Sprintf("closing the temp leaf failed: %v", err),
			tempPath, "finalizing the temp leaf before rename",
			"the destination leaf was not modified and the temp is being removed",
			"retry the leaf write", err,
		)
	}
	if err := os.Rename(tempPath, dest); err != nil {
		return cell.NewFault(
			"direct-file write", "atomic leaf rename",
			fmt.Sprintf("renaming the temp over the leaf failed: %v", err),
			dest, "committing the leaf",
			"the destination leaf is unchanged and the temp is being removed",
			"ensure the destination is writable, then retry", err,
		)
	}
	committed = true
	return nil
}

func freshTempPath(dir string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", cell.NewFault(
			"direct-file write", "fresh temp name",
			fmt.Sprintf("a random temp suffix could not be generated: %v", err),
			dir, "allocating a temp leaf",
			"the leaf write cannot begin",
			"retry the leaf write", err,
		)
	}
	return filepath.Join(dir, ".pasture-tmp-"+hex.EncodeToString(buf[:])), nil
}

func readBundleFile(bundle artifact.Bundle, name string) ([]byte, error) {
	file, err := bundle.Open(name)
	if err != nil {
		return nil, cell.NewFault(
			"direct-file ensure", "readable bundle leaf",
			fmt.Sprintf("the bundle leaf %q could not be opened: %v", name, err),
			name, "reading bundle bytes",
			"the leaf cannot be materialized",
			"report this as an internal bundle inconsistency", err,
		)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, cell.NewFault(
			"direct-file ensure", "readable bundle bytes",
			fmt.Sprintf("the bundle leaf %q could not be read: %v", name, err),
			name, "reading bundle bytes",
			"the leaf cannot be materialized",
			"report this as an internal bundle inconsistency", err,
		)
	}
	return content, nil
}

func rejectLeaf(path, reason, impact string) error {
	return cell.NewFault(
		"direct-file ensure", "safe managed leaf",
		reason, path, "verifying leaf ownership before mutation",
		impact,
		"move the conflicting entry aside, or reconcile the leaf through the installer, then retry", nil,
	)
}
