package effects

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// OSPublicationFS is the production PublicationFS backed by the os package. It
// carries no reconciliation policy; that lives entirely in Publish. Paths are
// used verbatim, so callers pass a payload root that is already resolved to a
// real filesystem location.
type OSPublicationFS struct{}

// NewOSPublicationFS returns the os-backed publication filesystem seam.
func NewOSPublicationFS() OSPublicationFS { return OSPublicationFS{} }

// Stat reports the node type and permission bits at target. A missing path is
// reported as NodeAbsent with a nil error. Symlinks and other irregular nodes
// are reported as NodeOther so publication treats them as unrelated drift rather
// than overwriting them.
func (OSPublicationFS) Stat(target string) (PublishedNode, error) {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PublishedNode{Type: NodeAbsent}, nil
		}
		return PublishedNode{}, err
	}
	mode := info.Mode()
	switch {
	case mode.IsRegular():
		return PublishedNode{Type: NodeFile, Mode: mode.Perm()}, nil
	case mode.IsDir():
		return PublishedNode{Type: NodeDir, Mode: mode.Perm()}, nil
	default:
		return PublishedNode{Type: NodeOther, Mode: mode.Perm()}, nil
	}
}

func (OSPublicationFS) ReadFile(target string) ([]byte, error) {
	return os.ReadFile(target)
}

func (OSPublicationFS) WriteFile(target string, content []byte, mode fs.FileMode) error {
	if err := os.WriteFile(target, content, mode); err != nil {
		return err
	}
	// Ensure the exact mode even when the file pre-existed with other bits.
	return os.Chmod(target, mode)
}

func (OSPublicationFS) MkdirAll(target string, mode fs.FileMode) error {
	return os.MkdirAll(filepath.Clean(target), mode)
}

func (OSPublicationFS) Remove(target string) error {
	return os.Remove(target)
}
