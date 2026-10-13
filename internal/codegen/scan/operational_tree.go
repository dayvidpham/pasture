package scan

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OperationalReport proves what was traversed, including compressed snapshots.
type OperationalReport struct {
	Files      int
	Candidates int
	Historical int
	TreeHash   string
}

// OperationalOutputRoots is the complete independent sweep boundary. Unlike
// the native Markdown scanner it includes dot trees and all embedded assets.
func OperationalOutputRoots() []string {
	return []string{"skills", "agents", "hooks", ".opencode", ".agents", ".codex",
		"internal/target/claudecode/assets", "internal/target/opencode/assets", "internal/target/codex/assets"}
}

// OperationalOwner maps only known byte-verbatim projections back to source.
// Other files own themselves and cannot borrow a historical disposition.
func OperationalOwner(file string) string {
	for _, prefix := range []string{".opencode/skill/", ".agents/skills/", "internal/target/opencode/assets/skills/", "internal/target/claudecode/assets/pasture-skills/skills/"} {
		if strings.HasPrefix(file, prefix) {
			return "skills/" + strings.TrimPrefix(file, prefix)
		}
	}
	return file
}

// CheckOperationalTree discovers every file beneath each supplied root,
// including dot trees and new operational files. No extension or testdata
// exclusion can hide an active file. Missing optional trees are empty; symlinks
// and unreadable files fail closed. The Codex snapshot is inspected decoded.
func CheckOperationalTree(root string, roots []string, step string) (OperationalReport, error) {
	files := map[string]bool{}
	for _, dir := range roots {
		base := filepath.Join(root, filepath.FromSlash(dir))
		if _, err := os.Lstat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink %q can conceal operational content", path)
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return OperationalReport{}, fmt.Errorf("operational tracker discovery failed at %s during %s: %w; stage outputs withheld; restore readable regular source files and regenerate", dir, step, err)
		}
	}
	keys := make([]string, 0, len(files))
	for file := range files {
		keys = append(keys, file)
	}
	sort.Strings(keys)
	var docs []OperationalDocument
	hash := sha256.New()
	for _, file := range keys {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return OperationalReport{}, fmt.Errorf("operational tracker read failed at %s during %s: %w; outputs withheld; restore readable source and regenerate", file, step, err)
		}
		fmt.Fprintf(hash, "%d:%s\x00%d:", len(file), file, len(content))
		_, _ = hash.Write(content)
		if strings.HasSuffix(file, ".json.gz") {
			decoded, err := trackerSnapshotDocuments(file, content)
			if err != nil {
				return OperationalReport{}, err
			}
			docs = append(docs, decoded...)
			continue
		}
		docs = append(docs, OperationalDocument{Owner: OperationalOwner(file), File: file, Content: content})
	}
	report := OperationalReport{Files: len(keys), TreeHash: hex.EncodeToString(hash.Sum(nil))}
	for _, doc := range docs {
		candidates, err := TrackerCandidates(doc)
		if err != nil {
			return report, err
		}
		report.Candidates += len(candidates)
	}
	history, err := OperationalHistory()
	if err != nil {
		return report, err
	}
	for _, dir := range roots {
		if dir != "skills" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, "skills")); os.IsNotExist(err) {
			continue // Native source-root validation diagnoses an absent root.
		}
		for _, entry := range history {
			if !files[entry.Owner] {
				return report, fmt.Errorf("operational tracker pinned historical owner %s was not discovered during %s; outputs withheld; restore the exact owner or review its retirement in internal/codegen/scan/manifest/operational_history.json and regenerate", entry.Owner, step)
			}
		}
	}
	if err := ValidateOperationalDocuments(docs, history, step); err != nil {
		return report, err
	}
	report.Historical = report.Candidates
	return report, nil
}

func trackerSnapshotDocuments(file string, content []byte) ([]OperationalDocument, error) {
	reader, err := gzip.NewReader(strings.NewReader(string(content)))
	if err != nil {
		return nil, fmt.Errorf("operational gate cannot decode %s: %w; outputs withheld; regenerate the snapshot", file, err)
	}
	decoded, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		return nil, fmt.Errorf("operational gate cannot read %s: read %v; close %v; outputs withheld; regenerate the snapshot", file, err, closeErr)
	}
	var wire []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(decoded, &wire); err != nil {
		return nil, fmt.Errorf("operational gate cannot parse %s: %w; outputs withheld; regenerate the snapshot", file, err)
	}
	var docs []OperationalDocument
	for _, item := range wire {
		docs = append(docs, OperationalDocument{Owner: OperationalOwner(item.Path), File: file + "/" + item.Path, Content: []byte(item.Content)})
	}
	return docs, nil
}
