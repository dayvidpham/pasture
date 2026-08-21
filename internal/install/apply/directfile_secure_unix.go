//go:build !windows

package apply

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// secureDirectTree keeps every pathname resolution relative to verified open
// directory descriptors. No inspection or mutation reopens an absolute child
// path after validation, so replacing a root or intermediate pathname cannot
// redirect the operation.
type secureDirectTree struct {
	root string
	fd   int
}

var errSecureRootAbsent = errors.New("direct-file destination root is absent")

func openSecureDirectTree(root string, create bool) (*secureDirectTree, error) {
	clean := filepath.Clean(root)
	if !filepath.IsAbs(clean) {
		return nil, directPathFault(root, "the destination root is relative", "configure an absolute destination root")
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, directPathFault(root, fmt.Sprintf("the filesystem root could not be anchored: %v", err), "repair filesystem access and retry")
	}
	current := string(filepath.Separator)
	for _, part := range splitNativePath(clean) {
		current = filepath.Join(current, part)
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) && !create {
			unix.Close(fd)
			return nil, errSecureRootAbsent
		}
		if errors.Is(openErr, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(fd, part, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(fd)
				return nil, directPathFault(current, fmt.Sprintf("a missing destination directory could not be created: %v", mkdirErr), "ensure the anchored ancestor is writable and retry")
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			unix.Close(fd)
			return nil, directPathFault(current, fmt.Sprintf("a destination boundary is missing, not a directory, or a symlink: %v", openErr), "replace every boundary with a real directory and retry")
		}
		unix.Close(fd)
		fd = next
	}
	return &secureDirectTree{root: clean, fd: fd}, nil
}

func (t *secureDirectTree) close() { _ = unix.Close(t.fd) }

func splitNativePath(value string) []string {
	trimmed := strings.TrimPrefix(filepath.Clean(value), string(filepath.Separator))
	if trimmed == "" || trimmed == "." {
		return nil
	}
	return strings.Split(trimmed, string(filepath.Separator))
}

func splitArtifactPath(rel string) ([]string, error) {
	clean := path.Clean(rel)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return nil, directPathFault(rel, "the artifact path does not name a contained leaf", "use a clean relative artifact path")
	}
	return strings.Split(clean, "/"), nil
}

func (t *secureDirectTree) parent(rel string, create bool, mode uint32) (int, string, []string, error) {
	parts, err := splitArtifactPath(rel)
	if err != nil {
		return -1, "", nil, err
	}
	fd, err := unix.Dup(t.fd)
	if err != nil {
		return -1, "", nil, err
	}
	var made []string
	for i, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) && !create {
			unix.Close(fd)
			return -1, "", nil, errSecureRootAbsent
		}
		if errors.Is(openErr, unix.ENOENT) && create {
			dirMode := mode
			if dirMode == 0 {
				dirMode = 0o755
			}
			if mkdirErr := unix.Mkdirat(fd, part, dirMode); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(fd)
				return -1, "", nil, directPathFault(filepath.Join(t.root, filepath.FromSlash(strings.Join(parts[:i+1], "/"))), fmt.Sprintf("a bundle directory could not be created: %v", mkdirErr), "repair directory permissions and retry")
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if openErr == nil {
				made = append(made, strings.Join(parts[:i+1], "/"))
			}
		}
		if openErr != nil {
			unix.Close(fd)
			return -1, "", nil, directPathFault(filepath.Join(t.root, filepath.FromSlash(strings.Join(parts[:i+1], "/"))), fmt.Sprintf("an intermediate destination is missing, not a directory, or a symlink: %v", openErr), "replace the boundary with a real directory and retry")
		}
		unix.Close(fd)
		fd = next
	}
	return fd, parts[len(parts)-1], made, nil
}

type secureIdentity struct {
	digest artifact.Digest
	mode   artifact.Mode
}

func readIdentityAt(parent int, name, location string) (secureIdentity, bool, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return secureIdentity{}, false, nil
	}
	if err != nil {
		return secureIdentity{}, false, directPathFault(location, fmt.Sprintf("the leaf could not be opened without following links: %v", err), "replace the leaf with a readable regular file and retry")
	}
	file := os.NewFile(uintptr(fd), location)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return secureIdentity{}, false, directPathFault(location, "the leaf is not a readable regular file", "move the conflicting entry aside and retry")
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return secureIdentity{}, false, directPathFault(location, fmt.Sprintf("the leaf could not be read: %v", err), "repair leaf permissions and retry")
	}
	mode, err := artifact.NewMode(uint32(info.Mode().Perm()))
	if err != nil {
		return secureIdentity{}, false, err
	}
	return secureIdentity{digest: artifact.DigestBytes(content), mode: mode}, true, nil
}

func writeLeafAt(parent int, name, location string, content []byte, mode uint32) error {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return directPathFault(location, fmt.Sprintf("a fresh temporary name could not be generated: %v", err), "retry the write")
	}
	temp := ".pasture-tmp-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(parent, temp, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return directPathFault(location, fmt.Sprintf("a create-exclusive temporary leaf could not be opened: %v", err), "repair directory permissions and retry")
	}
	file := os.NewFile(uintptr(fd), temp)
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = unix.Unlinkat(parent, temp, 0)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return directPathFault(location, fmt.Sprintf("temporary leaf bytes could not be written: %v", err), "repair filesystem space or permissions and retry")
	}
	if err := file.Chmod(os.FileMode(mode)); err != nil {
		return directPathFault(location, fmt.Sprintf("the exact leaf mode could not be set: %v", err), "repair filesystem mode support and retry")
	}
	if err := file.Sync(); err != nil {
		return directPathFault(location, fmt.Sprintf("temporary leaf bytes could not be flushed: %v", err), "repair filesystem durability support and retry")
	}
	if err := file.Close(); err != nil {
		return directPathFault(location, fmt.Sprintf("temporary leaf could not be closed: %v", err), "retry the write")
	}
	if err := unix.Renameat(parent, temp, parent, name); err != nil {
		return directPathFault(location, fmt.Sprintf("the temporary leaf could not be atomically renamed: %v", err), "repair directory permissions and retry")
	}
	committed = true
	return unix.Fsync(parent)
}

func (t *secureDirectTree) identity(rel string) (secureIdentity, bool, error) {
	parent, name, _, err := t.parent(rel, false, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, errSecureRootAbsent) {
			return secureIdentity{}, false, nil
		}
		return secureIdentity{}, false, err
	}
	defer unix.Close(parent)
	return readIdentityAt(parent, name, filepath.Join(t.root, filepath.FromSlash(rel)))
}

func (t *secureDirectTree) write(rel string, content []byte, mode uint32) ([]string, error) {
	parent, name, made, err := t.parent(rel, true, 0o755)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	if err := writeLeafAt(parent, name, filepath.Join(t.root, filepath.FromSlash(rel)), content, mode); err != nil {
		return nil, err
	}
	return made, nil
}

func (t *secureDirectTree) unlink(rel string) error {
	parent, name, _, err := t.parent(rel, false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if err := unix.Unlinkat(parent, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return directPathFault(filepath.Join(t.root, filepath.FromSlash(rel)), fmt.Sprintf("the managed leaf could not be unlinked: %v", err), "repair directory permissions and retry")
	}
	return nil
}

func (t *secureDirectTree) removeDir(rel string) (bool, error) {
	parent, name, _, err := t.parent(rel, false, 0)
	if err != nil {
		return false, nil
	}
	defer unix.Close(parent)
	err = unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return true, nil
	}
	if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) || errors.Is(err, unix.EACCES) {
		return false, nil
	}
	return false, directPathFault(filepath.Join(t.root, filepath.FromSlash(rel)), fmt.Sprintf("the recorded directory could not be removed safely: %v", err), "inspect the directory and retry")
}

func directPathFault(location, reason, fix string) error {
	return cell.NewFault("direct-file anchored I/O", "descriptor-relative no-follow path", reason, location, "inspecting or mutating a direct-file destination", "the operation was stopped rather than follow an unsafe or replaced pathname", fix, nil)
}
