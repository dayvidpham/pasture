package idmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// Backup is one consistent archive: where it is and its SHA-256.
type Backup struct {
	Path   string
	SHA256 string
}

// makeConsistentBackup writes a consistent, complete copy of the store —
// including everything currently in the WAL — through SQLite's own backup
// mechanism (VACUUM INTO), never a raw file copy: a raw copy of an open WAL
// database can miss committed pages still in the -wal file. The target name is
// unique and is never overwritten; VACUUM INTO itself refuses an existing
// target, and this function checks first so the error names the collision.
//
// The original store is retained as the byte-exact history archive: the active
// canonical bytes and their identity-dependent digests change representation
// during conversion, so the pre-conversion bytes live here.
func makeConsistentBackup(ctx context.Context, db *sql.DB, storePath, backupDir string, now time.Time) (Backup, error) {
	dir := backupDir
	if dir == "" {
		dir = filepath.Dir(storePath)
	}
	target, err := uniqueBackupPath(dir, filepath.Base(storePath), now)
	if err != nil {
		return Backup{}, err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return Backup{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't make the consistent backup of %q.", storePath),
			Why: "The SQLite backup (VACUUM INTO) failed. The conversion makes a consistent copy BEFORE it changes " +
				"anything, and it aborts when it cannot.",
			Where:  "Backing up the store before conversion (internal/idmigrate/backup.go in idmigrate.makeConsistentBackup).",
			Impact: "Nothing was converted. The store is unchanged and the conversion stops before its first write.",
			Fix: fmt.Sprintf("1. Confirm the target directory exists and is writable:\n     ls -ld %q\n"+
				"2. Confirm the disk has free space for a full copy of the store:\n     df -h %q\n"+
				"3. Retry once the backup can be written.", dir, dir),
			Cause: err,
		}
	}
	sum, err := fileSHA256(target)
	if err != nil {
		return Backup{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture made a backup at %q but couldn't checksum it.", target),
			Why:      "Reading the backup file to compute its SHA-256 failed.",
			Where:    "Backing up the store before conversion (internal/idmigrate/backup.go in idmigrate.makeConsistentBackup).",
			Impact:   "Nothing was converted; the conversion stops before its first write. The backup file itself is intact.",
			Fix:      "Confirm the backup file is readable, then retry.",
			Cause:    err,
		}
	}
	return Backup{Path: target, SHA256: sum}, nil
}

// uniqueBackupPath returns a backup path that does not exist yet. The name
// carries the store's base name and a UTC timestamp; a same-second collision
// gets a numeric suffix so the backup is never overwritten.
func uniqueBackupPath(dir, base string, now time.Time) (string, error) {
	stamp := now.UTC().Format("20060102T150405Z")
	for attempt := 0; ; attempt++ {
		name := fmt.Sprintf("%s.id-backup-%s", base, stamp)
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d", name, attempt)
		}
		candidate := filepath.Join(dir, name)
		switch _, err := os.Stat(candidate); {
		case os.IsNotExist(err):
			return candidate, nil
		case err != nil:
			return "", &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("Pasture couldn't check whether the backup path %q is free.", candidate),
				Why:      "The pre-backup existence check failed.",
				Where:    "Backing up the store before conversion (internal/idmigrate/backup.go in idmigrate.uniqueBackupPath).",
				Impact:   "Nothing was converted; the conversion stops before making the backup.",
				Fix:      "Confirm the backup directory is readable, then retry.",
				Cause:    err,
			}
		}
	}
}

// fileSHA256 returns the lower-case hex SHA-256 of a file's bytes.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
