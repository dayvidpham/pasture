package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// HashTree computes a deterministic content digest over every non-excluded
// file under every canonical root (see discoverAllFiles), independent of
// file extension. Calling HashTree before and after a scan and comparing the
// two results is pasture#47's required byte-for-byte read-only proof: any
// change to any byte, any added/removed/renamed file, or any permission-
// affecting rewrite that changes content under a canonical root changes the
// digest.
//
// Boundary: this proof covers exactly the same tree discoverAllFiles walks —
// every non-excluded file under a canonical root — and nothing else:
//   - content of an excluded segment (testdata/vendor/.git/.opencode) is
//     never read or hashed, so a scan bug that wrote into
//     skills/.opencode/* or skills/testdata/* during ScanWithManifests would
//     not change the digest and would not be caught by this proof;
//   - file mode/permission bits are not part of the digest — only file
//     content and path are — so a permission-only change (with identical
//     content) is likewise invisible to this proof.
//
// Both are acceptable for this proof's purpose (detecting the scanner
// mutating canonical *source* content), but a caller must not read "hash
// every canonical source tree byte-for-byte" as covering excluded content or
// permission bits.
func HashTree(baseDir string, roots []string) (string, error) {
	files, err := discoverAllFiles(baseDir, roots)
	if err != nil {
		return "", err
	}

	hasher := sha256.New()
	for _, relPath := range files { // discoverAllFiles returns sorted, deterministic order
		content, err := os.ReadFile(filepath.Join(baseDir, relPath))
		if err != nil {
			return "", diagnostic(
				fmt.Sprintf("could not read %q while hashing the canonical tree", relPath),
				"the read-only byte-for-byte proof requires reading every discovered file's exact content",
				"scan.HashTree:"+relPath, "read-only tree hashing",
				"the scan cannot prove it left the canonical tree unmodified",
				"resolve the filesystem error and re-run the scan",
				err,
			)
		}
		fmt.Fprintf(hasher, "%d:%s\x00%d:", len(relPath), relPath, len(content))
		hasher.Write(content)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
