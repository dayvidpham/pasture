//go:build linux || darwin

package registry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const maxRegistryBytes = 8 << 20

// openRegistryParent anchors at the filesystem root and opens every parent one
// component at a time with O_NOFOLLOW. Directory replacement after a component
// is opened cannot redirect later descriptor-relative operations.
func openRegistryParent(path string) (int, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return -1, "", err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(filepath.Dir(abs)), string(filepath.Separator)), string(filepath.Separator))
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			_ = unix.Close(fd)
			return -1, "", fmt.Errorf("open non-symlink parent component %q: %w", part, openErr)
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, filepath.Base(abs), nil
}

func readRegistryFile(path string) ([]byte, os.FileInfo, error) {
	parent, base, err := openRegistryParent(path)
	if err != nil {
		return nil, nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("construct descriptor handle for %q", path)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("registry descriptor has unsafe type %s, not a regular file", info.Mode().Type())
	}
	if info.Mode().Perm() != 0o600 {
		return nil, nil, fmt.Errorf("registry descriptor has mode %04o, not required mode 0600", info.Mode().Perm())
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
	parent, base, err := openRegistryParent(path)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if existing, openErr := unix.Openat(parent, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0); openErr == nil {
		var stat unix.Stat_t
		statErr := unix.Fstat(existing, &stat)
		_ = unix.Close(existing)
		if statErr != nil {
			return statErr
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return fmt.Errorf("destination is not a regular file")
		}
	} else if !os.IsNotExist(openErr) {
		return fmt.Errorf("inspect no-follow destination: %w", openErr)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate exclusive temporary name: %w", err)
	}
	temp := ".pasture-registry-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(parent, temp, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("create exclusive temporary file: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = unix.Unlinkat(parent, temp, 0)
		}
	}()
	file := os.NewFile(uintptr(fd), temp)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("construct temporary descriptor")
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary file mode 0600: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary registry: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("flush temporary registry: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary registry: %w", err)
	}
	if err := registryUnixRenameat(parent, temp, parent, base); err != nil {
		return fmt.Errorf("atomically replace registry: %w", err)
	}
	committed = true
	if err := unix.Fsync(parent); err != nil {
		return fmt.Errorf("flush committed registry directory: %w", err)
	}
	return nil
}

var registryUnixRenameat = unix.Renameat
