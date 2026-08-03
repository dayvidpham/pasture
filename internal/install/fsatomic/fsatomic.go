// Package fsatomic performs symlink-safe atomic file replacement for the
// installer's preference and confirmed-state files.
//
// A write uses one same-directory create-exclusive temp, flushes the bytes,
// atomically renames over the destination, and flushes the parent directory.
// The live process best-effort removes only the exact temp it created. A crash
// orphan is never scanned, deleted, reused, or treated as recovery input; a
// later save allocates a fresh random temp and the committed file remains
// authoritative.
//
// The destination must be a regular file or absent. A symlink or other unsafe
// destination type fails before any write, so a hostile link can never redirect
// a Pasture write outside the intended path.
package fsatomic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/install/cell"
)

// WriteFile atomically replaces path with data at the given mode. The parent
// directory must already exist.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := checkSafeDestination(path); err != nil {
		return err
	}

	tempPath, tempFile, err := createExclusiveTemp(dir, mode)
	if err != nil {
		return err
	}
	// From here the live process owns exactly tempPath and must clean it up on
	// any failure after this point.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return cell.NewFault(
			"state write", "temp bytes flushed",
			fmt.Sprintf("writing the payload to the temp file failed: %v", err),
			tempPath, "flushing bytes before atomic rename",
			"the destination was not modified and the temp is being removed",
			"ensure the target directory has space and write permission, then retry", err,
		)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return cell.NewFault(
			"state write", "temp durable",
			fmt.Sprintf("syncing the temp file failed: %v", err),
			tempPath, "flushing bytes to stable storage",
			"the destination was not modified and the temp is being removed",
			"ensure the filesystem supports fsync and has space, then retry", err,
		)
	}
	if err := tempFile.Close(); err != nil {
		return cell.NewFault(
			"state write", "temp closed",
			fmt.Sprintf("closing the temp file failed: %v", err),
			tempPath, "finalizing the temp file before rename",
			"the destination was not modified and the temp is being removed",
			"retry the save; if it persists inspect the filesystem", err,
		)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return cell.NewFault(
			"state write", "atomic rename",
			fmt.Sprintf("renaming the temp over the destination failed: %v", err),
			path, "committing the new file",
			"the previous file (if any) is unchanged and the temp is being removed",
			"ensure the destination is on the same filesystem and writable, then retry", err,
		)
	}
	committed = true
	return syncDir(dir)
}

func checkSafeDestination(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return cell.NewFault(
			"state write", "destination inspectable",
			fmt.Sprintf("the destination could not be inspected: %v", err),
			path, "checking the destination type before writing",
			"a write could clobber or follow an unexpected path type",
			"ensure the destination path is accessible, then retry", err,
		)
	}
	if info.Mode().Type()&fs.ModeSymlink != 0 {
		return cell.NewFault(
			"state write", "regular-file or absent destination",
			"the destination is a symlink",
			path, "checking the destination type before writing",
			"following the link could redirect a Pasture write outside its intended location",
			"remove the symlink and let Pasture manage a regular file at this path", nil,
		)
	}
	if !info.Mode().IsRegular() {
		return cell.NewFault(
			"state write", "regular-file or absent destination",
			fmt.Sprintf("the destination is a %s, not a regular file", info.Mode().Type()),
			path, "checking the destination type before writing",
			"Pasture will not overwrite a non-regular file type",
			"move the conflicting entry aside so Pasture can manage a regular file here", nil,
		)
	}
	return nil
}

func createExclusiveTemp(dir string, mode os.FileMode) (string, *os.File, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", nil, cell.NewFault(
			"state write", "fresh temp name",
			fmt.Sprintf("a random temp suffix could not be generated: %v", err),
			dir, "allocating a create-exclusive temp file",
			"the atomic write cannot begin",
			"retry the save", err,
		)
	}
	tempPath := filepath.Join(dir, fmt.Sprintf(".pasture-tmp-%s", hex.EncodeToString(buf[:])))
	file, err := os.OpenFile(tempPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return "", nil, cell.NewFault(
			"state write", "create-exclusive temp",
			fmt.Sprintf("the temp file could not be created exclusively: %v", err),
			tempPath, "allocating a create-exclusive temp file",
			"the atomic write cannot begin and nothing was modified",
			"ensure the directory exists and is writable, then retry", err,
		)
	}
	return tempPath, file, nil
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		// The rename already committed; a directory flush failure is reported
		// but the committed file remains authoritative.
		return cell.NewFault(
			"state write", "directory durable",
			fmt.Sprintf("the parent directory could not be opened to flush: %v", err),
			dir, "flushing the directory entry after commit",
			"the new file is committed but its directory entry may not be durable across a crash",
			"the save succeeded; no action is required unless the machine crashes before the next sync", err,
		)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return cell.NewFault(
			"state write", "directory durable",
			fmt.Sprintf("the parent directory could not be flushed: %v", err),
			dir, "flushing the directory entry after commit",
			"the new file is committed but its directory entry may not be durable across a crash",
			"the save succeeded; no action is required unless the machine crashes before the next sync", err,
		)
	}
	return nil
}
