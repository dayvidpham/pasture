package export

import (
	"bytes"
	"fmt"
	"io/fs"

	"github.com/dayvidpham/pasture/artifact"
)

// VerifyArchive re-reads archive bytes and proves they carry exactly the
// members, permission modes, sizes, and content digests the bundle manifest
// declares — in the canonical order. It is the round-trip check every written
// archive passes before an export is reported as complete.
func VerifyArchive(id artifact.ComponentID, location string, content []byte, bundle artifact.Bundle) error {
	expected, err := BundleMembers(bundle)
	if err != nil {
		return err
	}
	return verifyMembers(id, location, content, expected)
}

func verifyMembers(id artifact.ComponentID, location string, content []byte, expected []Member) error {
	actual, err := ReadArchive(bytes.NewReader(content))
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return archiveFault(
			"component archive verification", "the archive carries exactly the manifest entries",
			fmt.Sprintf("archive %q for component %s holds %d members but its bundle manifest declares %d",
				location, id, len(actual), len(expected)),
			"the published component would not match the target descriptor it claims to carry",
			"rebuild the export from the embedded target descriptors and do not publish this directory", fs.ErrInvalid)
	}
	for index, want := range expected {
		got := actual[index]
		if got.Path != want.Path || got.Kind != want.Kind || got.Mode != want.Mode || got.Size != want.Size || got.Digest != want.Digest {
			return archiveFault(
				"component archive verification", "every member matches its manifest entry exactly",
				fmt.Sprintf("archive %q for component %s holds member %d as %s %q mode %04o size %d digest %s but the manifest declares %s %q mode %04o size %d digest %s",
					location, id, index, got.Kind, got.Path, got.Mode, got.Size, got.Digest,
					want.Kind, want.Path, want.Mode, want.Size, want.Digest),
				"the published component would not match the target descriptor it claims to carry",
				"rebuild the export from the embedded target descriptors and do not publish this directory", fs.ErrInvalid)
		}
	}
	return nil
}
