//go:build windows

package registry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxRegistryBytes = 8 << 20

// openRegistryParentWindows anchors traversal at the volume root. os.Root is
// implemented with Windows directory handles and remains confined under
// concurrent filesystem layout changes. Each stable reparse boundary is
// rejected before opening the next rooted directory.
func openRegistryParentWindows(path string) (*os.Root, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	volume := filepath.VolumeName(abs)
	if volume == "" {
		return nil, "", fmt.Errorf("registry path %q has no Windows volume", path)
	}
	root, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(volume+string(filepath.Separator), filepath.Dir(abs))
	if err != nil {
		root.Close()
		return nil, "", err
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		info, statErr := root.Lstat(part)
		if statErr != nil {
			root.Close()
			return nil, "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			root.Close()
			return nil, "", fmt.Errorf("parent component %q is a reparse link or non-directory", part)
		}
		next, openErr := root.OpenRoot(part)
		if openErr != nil {
			root.Close()
			return nil, "", openErr
		}
		opened, openedErr := next.Lstat(".")
		if openedErr != nil || !os.SameFile(info, opened) {
			next.Close()
			root.Close()
			return nil, "", fmt.Errorf("parent component %q changed while opening its directory handle", part)
		}
		root.Close()
		root = next
	}
	return root, filepath.Base(abs), nil
}

func readRegistryFile(path string) ([]byte, os.FileInfo, error) {
	root, base, err := openRegistryParentWindows(path)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	entry, err := root.Lstat(base)
	if err != nil {
		return nil, nil, err
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("registry path is a reparse link")
	}
	file, err := root.Open(base)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(entry, info) {
		return nil, nil, fmt.Errorf("registry entry changed while opening its file handle")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, nil, fmt.Errorf("registry descriptor must be a regular mode-0600 file; got %s/%04o", info.Mode().Type(), info.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRegistryBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxRegistryBytes {
		return nil, nil, fmt.Errorf("registry exceeds the %d-byte input limit", maxRegistryBytes)
	}
	return data, info, nil
}

func writeRegistryFile(path string, data []byte) error {
	root, base, err := openRegistryParentWindows(path)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, statErr := root.Lstat(base); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("registry destination is a reparse link or non-regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	temp := ".pasture-registry-" + hex.EncodeToString(suffix[:])
	file, err := root.OpenFile(temp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(temp)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(temp, base); err != nil {
		return err
	}
	committed = true
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
