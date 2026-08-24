package export_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/export"
)

func writeArchive(t *testing.T, bundle artifact.Bundle) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := export.WriteArchive(&buffer, bundle); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return buffer.Bytes()
}

// The format contract is byte-identical output for the same input, so a release
// can be rebuilt from the same revision and compared.
func TestWriteArchive_IsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	bundle := newBundle(t, cellLeaves(artifact.ComponentIDs()[0])...)
	first := writeArchive(t, bundle)
	second := writeArchive(t, bundle)
	if !bytes.Equal(first, second) {
		t.Fatalf("archive bytes differ across runs: %d vs %d bytes", len(first), len(second))
	}
	// A second bundle value built from identical declarations must also produce
	// identical bytes, proving nothing run-specific leaks into the archive.
	third := writeArchive(t, newBundle(t, cellLeaves(artifact.ComponentIDs()[0])...))
	if !bytes.Equal(first, third) {
		t.Fatal("archive bytes differ between equal bundles")
	}
}

func TestWriteArchive_MembersMatchBundleManifest(t *testing.T) {
	t.Parallel()
	bundle := newBundle(t,
		leaf{path: "b/second.md", mode: 0o644, body: "second\n"},
		leaf{path: "a/first.sh", mode: 0o755, body: "#!/bin/sh\n"},
		leaf{path: "top.json", mode: 0o600, body: "{}\n"},
	)
	members, err := export.ReadArchive(bytes.NewReader(writeArchive(t, bundle)))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	expected, err := export.BundleMembers(bundle)
	if err != nil {
		t.Fatalf("bundle members: %v", err)
	}
	if len(members) != len(expected) {
		t.Fatalf("read %d members, manifest declares %d", len(members), len(expected))
	}
	for index, want := range expected {
		if members[index] != want {
			t.Fatalf("member %d: read %+v, manifest declares %+v", index, members[index], want)
		}
	}
	wantOrder := []string{"a/first.sh", "b/second.md", "top.json"}
	for index, want := range wantOrder {
		if members[index].Path != want {
			t.Fatalf("member %d is %q, want lexicographic order %q", index, members[index].Path, want)
		}
	}
	if members[0].Mode != 0o755 || members[1].Mode != 0o644 || members[2].Mode != 0o600 {
		t.Fatalf("modes were not taken from the manifest: %04o %04o %04o", members[0].Mode, members[1].Mode, members[2].Mode)
	}
}

// Ownership and timestamps must not carry the building machine's identity.
func TestWriteArchive_ZeroesOwnershipAndTimestamps(t *testing.T) {
	t.Parallel()
	bundle := newBundle(t, leaf{path: "only.md", mode: 0o644, body: "only\n"})
	decompressor, err := gzip.NewReader(bytes.NewReader(writeArchive(t, bundle)))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer decompressor.Close()
	if decompressor.Header.Name != "" || decompressor.Header.Comment != "" {
		t.Fatalf("gzip header carries a name %q or comment %q", decompressor.Header.Name, decompressor.Header.Comment)
	}
	if !decompressor.Header.ModTime.IsZero() && decompressor.Header.ModTime.Unix() != 0 {
		t.Fatalf("gzip header carries modification time %s", decompressor.Header.ModTime)
	}
	reader := tar.NewReader(decompressor)
	header, err := reader.Next()
	if err != nil {
		t.Fatalf("tar member: %v", err)
	}
	if header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
		t.Fatalf("member carries ownership uid=%d gid=%d uname=%q gname=%q", header.Uid, header.Gid, header.Uname, header.Gname)
	}
	if !header.ModTime.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("member modification time is %s, want the fixed epoch", header.ModTime)
	}
}

func TestVerifyArchive_RejectsMutatedBytes(t *testing.T) {
	t.Parallel()
	id := artifact.ComponentIDs()[0]
	bundle := newBundle(t, leaf{path: "only.md", mode: 0o644, body: "only\n"})
	content := writeArchive(t, bundle)
	if err := export.VerifyArchive(id, "in-memory", content, bundle); err != nil {
		t.Fatalf("verify unmodified archive: %v", err)
	}
	// Rebuild the same layout with different bytes: the archive no longer
	// matches the manifest it claims to carry.
	other := newBundle(t, leaf{path: "only.md", mode: 0o644, body: "tampered\n"})
	err := export.VerifyArchive(id, "in-memory", writeArchive(t, other), bundle)
	if err == nil {
		t.Fatal("verification accepted an archive whose bytes differ from the manifest")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Fatalf("verification failure is not actionable about the digest: %v", err)
	}
}

func TestVerifyArchive_RejectsExtraMember(t *testing.T) {
	t.Parallel()
	id := artifact.ComponentIDs()[0]
	bundle := newBundle(t, leaf{path: "only.md", mode: 0o644, body: "only\n"})
	extra := newBundle(t,
		leaf{path: "only.md", mode: 0o644, body: "only\n"},
		leaf{path: "smuggled.md", mode: 0o644, body: "smuggled\n"},
	)
	if err := export.VerifyArchive(id, "in-memory", writeArchive(t, extra), bundle); err == nil {
		t.Fatal("verification accepted an archive with an undeclared member")
	}
}

func TestReadArchive_RejectsUnsupportedMemberTypes(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	compressor := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressor)
	header := &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape", Mode: 0o777, ModTime: time.Unix(0, 0).UTC()}
	if err := archive.WriteHeader(header); err != nil {
		t.Fatalf("write symlink header: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	_, err := export.ReadArchive(bytes.NewReader(buffer.Bytes()))
	if err == nil {
		t.Fatal("read accepted a symlink member")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("failure does not explain the unsupported member: %v", err)
	}
}

func TestReadArchive_RejectsNonArchiveBytes(t *testing.T) {
	t.Parallel()
	if _, err := export.ReadArchive(strings.NewReader("not an archive")); err == nil {
		t.Fatal("read accepted bytes that are not a gzip stream")
	}
}

// repackage rewrites an archive through the standard library, applying an
// optional per-member header mutation. It builds the tamper cases without
// going through the export writer, so verification is tested against bytes the
// export path would never produce.
func repackage(t *testing.T, content []byte, mutate func(*tar.Header)) []byte {
	t.Helper()
	decompressor, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer decompressor.Close()
	reader := tar.NewReader(decompressor)
	var buffer bytes.Buffer
	compressor := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressor)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read member: %v", err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read member body: %v", err)
		}
		rewritten := *header
		if mutate != nil {
			mutate(&rewritten)
		}
		if err := writer.WriteHeader(&rewritten); err != nil {
			t.Fatalf("write member header: %v", err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("write member body: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

// A member whose permission bits diverge from the manifest is a different
// artifact: an executable hook that arrives as 0644 silently fails to run.
func TestVerifyArchive_RejectsMutatedMode(t *testing.T) {
	t.Parallel()
	id := artifact.ComponentIDs()[0]
	bundle := newBundle(t,
		leaf{path: "run.sh", mode: 0o755, body: "#!/bin/sh\necho hello\n"},
		leaf{path: "notes.md", mode: 0o644, body: "notes\n"},
	)
	content := writeArchive(t, bundle)
	if err := export.VerifyArchive(id, "in-memory", content, bundle); err != nil {
		t.Fatalf("verify unmodified archive: %v", err)
	}
	tampered := repackage(t, content, func(header *tar.Header) {
		if header.Name == "run.sh" {
			header.Mode = 0o644
		}
	})
	err := export.VerifyArchive(id, "in-memory", tampered, bundle)
	if err == nil {
		t.Fatal("verification accepted an archive whose member mode differs from the manifest")
	}
	if !strings.Contains(err.Error(), "0644") || !strings.Contains(err.Error(), "0755") {
		t.Fatalf("verification failure does not name both modes: %v", err)
	}
}

// Reordered members are also a mismatch: the format pins lexicographic order so
// two builds of one bundle compare byte for byte.
func TestVerifyArchive_RejectsReorderedMembers(t *testing.T) {
	t.Parallel()
	id := artifact.ComponentIDs()[0]
	bundle := newBundle(t,
		leaf{path: "alpha.md", mode: 0o644, body: "alpha\n"},
		leaf{path: "beta.md", mode: 0o644, body: "beta\n"},
	)
	content := writeArchive(t, bundle)
	reordered := reverseMembers(t, content)
	if err := export.VerifyArchive(id, "in-memory", reordered, bundle); err == nil {
		t.Fatal("verification accepted an archive whose members are out of canonical order")
	}
}

func reverseMembers(t *testing.T, content []byte) []byte {
	t.Helper()
	decompressor, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer decompressor.Close()
	reader := tar.NewReader(decompressor)
	type member struct {
		header tar.Header
		body   []byte
	}
	members := make([]member, 0, 4)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read member: %v", err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read member body: %v", err)
		}
		members = append(members, member{header: *header, body: body})
	}
	var buffer bytes.Buffer
	compressor := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressor)
	for index := len(members) - 1; index >= 0; index-- {
		if err := writer.WriteHeader(&members[index].header); err != nil {
			t.Fatalf("write member header: %v", err)
		}
		if _, err := writer.Write(members[index].body); err != nil {
			t.Fatalf("write member body: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

// goldenSyntheticArchiveDigest pins the exact bytes the format produces for the
// fixture bundle below. A change here means the archive bytes changed: either
// this package changed the format (which must be a deliberate, documented
// change), or the Go toolchain's DEFLATE implementation changed, which
// legitimately updates this constant — see the determinism section of
// docs/component-archive-format.md.
const goldenSyntheticArchiveDigest = "sha256:8c62445d35421a81c5bf32e0499485e5811f8986c01f5016dfaa1b42d240e82c"

func TestWriteArchive_MatchesTheGoldenDigest(t *testing.T) {
	t.Parallel()
	bundle := newBundle(t,
		leaf{path: "alpha/nested.json", mode: 0o644, body: "{\"cell\":\"golden\"}\n"},
		leaf{path: "alpha/run.sh", mode: 0o755, body: "#!/bin/sh\necho golden\n"},
		leaf{path: "zebra.md", mode: 0o644, body: "zebra for golden\n"},
	)
	digest := artifact.DigestBytes(writeArchive(t, bundle))
	if digest.String() != goldenSyntheticArchiveDigest {
		t.Fatalf("archive digests to %s, want the pinned %s; if this follows a deliberate format change or a Go toolchain flate change, update the constant and say so in the commit",
			digest, goldenSyntheticArchiveDigest)
	}
}
