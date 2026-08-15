//go:build windows

package apply

import (
	"errors"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// Windows support must use an equivalent handle-relative reparse-point-safe
// implementation before DirectFile activation is enabled there. Failing closed
// is safer than reopening path strings after validation.
type secureDirectTree struct{}
type secureIdentity struct {
	digest artifact.Digest
	mode   artifact.Mode
}

var errSecureRootAbsent = errors.New("direct-file destination root is absent")

func openSecureDirectTree(root string, create bool) (*secureDirectTree, error) {
	return nil, cell.NewFault("direct-file anchored I/O", "handle-relative no-follow path", "this build has no reparse-point-safe DirectFile implementation", root, "opening a DirectFile destination", "no destination was inspected or mutated", "run DirectFile activation on a supported Unix host", nil)
}
func (*secureDirectTree) close() {}
func (*secureDirectTree) identity(string) (secureIdentity, bool, error) {
	return secureIdentity{}, false, errors.New("unsupported")
}
func (*secureDirectTree) write(string, []byte, uint32) ([]string, error) {
	return nil, errors.New("unsupported")
}
func (*secureDirectTree) unlink(string) error            { return errors.New("unsupported") }
func (*secureDirectTree) removeDir(string) (bool, error) { return false, errors.New("unsupported") }
func directPathFault(location, reason, fix string) error {
	return cell.NewFault("direct-file anchored I/O", "handle-relative no-follow path", reason, location, "inspecting or mutating a direct-file destination", "the operation was stopped rather than follow an unsafe or replaced pathname", fix, nil)
}
