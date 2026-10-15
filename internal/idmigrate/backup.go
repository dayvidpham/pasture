package idmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// Archive is the P1 result: where the consistent archive is, its SHA-256, and
// the archive's OBSERVED identity-format marker. The marker is the fact the
// scoped P3a refusal states; it is read at the archive's verify reopen and is
// never inferred from timing.
type Archive struct {
	Path   string
	SHA256 string
	// Skipped is true when the store did not exist at P1's own check, so no
	// archive was made (archive_path is empty and no pre-cutover content is
	// recoverable through this run's artifacts).
	Skipped bool
	Marker  ArchiveMarker
}

// stagingSeq makes every archive staging name unique within this process.
// Combined with the PID below, no two archivers ever share a staging file:
// not two pastured processes stamping the same UTC second on one store's
// first open, and not two concurrent attempts in one process either.
var stagingSeq atomic.Uint64

// ArchiveStore runs P1: a consistent logical copy of the committed store,
// taken through a READ-ONLY source connection so no journal-mode switch or any
// other write precedes it. The copy is staged as `<final>.<pid>.<seq>.partial`,
// fsynced, verified (integrity_check + foreign_key_check + the format marker
// + SHA-256), and only then published to its final name with a link that
// refuses to overwrite another archive. An unverified file can never sit at
// the name a recovery instruction points at, and one archiver can never
// clear, overwrite, or rename away another's bytes mid-flight.
//
// It is skipped only when the store did not exist at this function's own
// read-only observation of the path: the first open of a brand-new store has no
// pre-cutover content to archive.
func ArchiveStore(ctx context.Context, storePath, backupDir string, now time.Time) (Archive, error) {
	if !StatStore(storePath) {
		return Archive{Skipped: true}, nil
	}

	source, err := dbconn.OpenReadOnlyDB(storePath)
	if err != nil {
		return Archive{}, archiveError(storePath, "Pasture couldn't open the store read-only to archive it.", err)
	}
	defer source.Close()

	dir := backupDir
	if dir == "" {
		dir = filepath.Dir(storePath)
	}
	final, err := uniqueBackupPath(dir, filepath.Base(storePath), now)
	if err != nil {
		return Archive{}, err
	}
	partial := fmt.Sprintf("%s.%d.%d.partial", final, os.Getpid(), stagingSeq.Add(1))
	// The staging name is fresh per attempt, so it cannot pre-exist: VACUUM
	// INTO either writes it or fails cleanly, and no stale cleanup runs that
	// could delete another archiver's live file. (Only this attempt knows
	// this name; a staging file left by a killed run lingers harmlessly
	// beside the store instead of risking a live attempt.) Legacy
	// `<final>.partial` files from previous versions are never touched.

	if _, err := source.ExecContext(ctx, `VACUUM INTO ?`, partial); err != nil {
		return Archive{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't make the consistent archive of %q.", storePath),
			Why: "The SQLite archive (VACUUM INTO) failed. The cutover makes a consistent copy BEFORE it changes " +
				"anything, and it aborts when it cannot.",
			Where:  "Archiving the store before conversion (internal/idmigrate/backup.go in idmigrate.ArchiveStore).",
			Impact: "Nothing was converted. The store is unchanged and the cutover stops before its first write.",
			Fix: fmt.Sprintf("1. Confirm the target directory exists and is writable:\n     ls -ld %q\n"+
				"2. Confirm the disk has free space for a full copy of the store:\n     df -h %q\n"+
				"3. Retry once the archive can be written.", dir, dir),
			Cause: err,
		}
	}
	if err := fsyncPath(partial); err != nil {
		_ = os.Remove(partial)
		return Archive{}, archiveError(storePath, "Pasture couldn't flush the staged archive to disk.", err)
	}

	marker, verifyErr := verifyArchive(ctx, partial)
	if verifyErr != nil {
		_ = os.Remove(partial)
		return Archive{}, verifyErr
	}
	sum, err := fileSHA256(partial)
	if err != nil {
		_ = os.Remove(partial)
		return Archive{}, archiveError(storePath, "Pasture made a staged archive but couldn't checksum it.", err)
	}

	published, err := publishArchive(storePath, partial, final)
	if err != nil {
		_ = os.Remove(partial)
		return Archive{}, err
	}
	// The staging name was this attempt's alone: drop it now that the same
	// bytes are linked at the final name, then flush the directory so both
	// the publish and the staging removal are durable.
	if err := os.Remove(partial); err != nil {
		return Archive{}, archiveError(storePath, "Pasture couldn't clear the published staging archive.", err)
	}
	if err := fsyncPath(dir); err != nil {
		return Archive{}, archiveError(storePath, "Pasture couldn't flush the archive directory.", err)
	}
	return Archive{Path: published, SHA256: sum, Marker: marker}, nil
}

// publishArchive moves the verified staging file to its final archive name
// without ever overwriting another archive. os.Link publishes atomically
// only when the target does not exist; on EEXIST — a concurrent archiver
// published the same timestamped name first, or a previous archive is already
// there — it retries with the same numeric suffixes uniqueBackupPath uses,
// linking the already-verified bytes under each candidate in turn. The
// staging file is this attempt's alone, so no other process can take it
// between verification and publish.
func publishArchive(storePath, partial, final string) (string, error) {
	for attempt := 0; ; attempt++ {
		target := final
		if attempt > 0 {
			target = fmt.Sprintf("%s-%d", final, attempt)
		}
		if err := os.Link(partial, target); err == nil {
			return target, nil
		} else if !os.IsExist(err) {
			return "", archiveError(storePath, "Pasture couldn't move the verified archive into place.", err)
		}
	}
}

// verifyArchive reopens the staged archive read-only and proves it is a
// consistent, complete, markable SQLite file: integrity_check,
// foreign_key_check, and the identity-format marker the refusal will state.
func verifyArchive(ctx context.Context, path string) (ArchiveMarker, error) {
	db, err := dbconn.OpenReadOnlyDB(path)
	if err != nil {
		return ArchiveMarker{}, archiveError(path, "Pasture couldn't reopen the staged archive to verify it.", err)
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return ArchiveMarker{}, archiveError(path, "Pasture couldn't run integrity_check on the staged archive.", err)
	}
	if integrity != "ok" {
		return ArchiveMarker{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The staged archive %q failed SQLite integrity_check.", path),
			Why:      fmt.Sprintf("integrity_check reported %q instead of \"ok\".", integrity),
			Where:    "Verifying the staged archive (internal/idmigrate/backup.go in idmigrate.verifyArchive).",
			Impact:   "The cutover aborts before its first write; the store is unchanged and no unverified archive is kept.",
			Fix:      "1. Confirm the source store is healthy:\n     sqlite3 <store> 'PRAGMA integrity_check'\n2. Retry once the source is healthy.",
		}
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return ArchiveMarker{}, archiveError(path, "Pasture couldn't run foreign_key_check on the staged archive.", err)
	}
	hasRows := rows.Next()
	rows.Close()
	if err := rows.Err(); err != nil {
		return ArchiveMarker{}, archiveError(path, "Pasture couldn't read the staged archive's foreign-key check.", err)
	}
	if hasRows {
		return ArchiveMarker{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The staged archive %q failed SQLite foreign_key_check.", path),
			Why:      "The archive contains rows that violate a foreign-key constraint, so it is not a reliable rollback source.",
			Where:    "Verifying the staged archive (internal/idmigrate/backup.go in idmigrate.verifyArchive).",
			Impact:   "The cutover aborts before its first write; the store is unchanged.",
			Fix:      "1. Repair the reported foreign-key violation on the source store, then retry.\n2. Report the violation if the source passes its own checks.",
		}
	}
	return readArchiveMarker(ctx, db)
}

// uniqueBackupPath returns an archive path that does not exist yet. The name
// carries the store's base name and a UTC timestamp; a same-second collision
// gets a numeric suffix so the archive is never overwritten.
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
				What:     fmt.Sprintf("Pasture couldn't check whether the archive path %q is free.", candidate),
				Why:      "The pre-archive existence check failed.",
				Where:    "Archiving the store before conversion (internal/idmigrate/backup.go in idmigrate.uniqueBackupPath).",
				Impact:   "Nothing was converted; the cutover stops before making the archive.",
				Fix:      "Confirm the archive directory is readable, then retry.",
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

// fsyncPath flushes path's bytes (or, for a directory, its entries) to stable
// storage. A best-effort operation: a failure is reported so the caller can
// abort rather than commit beside an unfsynced archive.
func fsyncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func archiveError(path, what string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("%s (%q)", what, path),
		Why:      cause.Error(),
		Where:    "Archiving the store before conversion (internal/idmigrate/backup.go).",
		Impact:   "Nothing was converted; the cutover stops before its first write and the store is unchanged.",
		Fix:      "1. Confirm the store and its directory are readable and have free space.\n2. Retry once the underlying problem is fixed.",
		Cause:    cause,
	}
}
