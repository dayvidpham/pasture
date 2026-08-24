package export

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"time"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// ArchiveFormat is the canonical identifier of the component archive format
// specified in docs/component-archive-format.md.
const ArchiveFormat = "pasture.component-archive/v1"

// archiveEpoch is the single timestamp every archive member carries. A fixed
// epoch (not the zero time.Time, which is not representable in a portable tar
// header) keeps byte-identical output across runs and machines.
var archiveEpoch = time.Unix(0, 0).UTC()

// MemberKind is the closed set of archive member variants.
type MemberKind uint8

const (
	// MemberDirectory is a directory member declared by the bundle manifest.
	MemberDirectory MemberKind = iota + 1
	// MemberRegularFile is a regular-file member declared by the bundle manifest.
	MemberRegularFile
)

func (k MemberKind) String() string {
	switch k {
	case MemberDirectory:
		return "directory"
	case MemberRegularFile:
		return "regular-file"
	default:
		return "unknown"
	}
}

// Member is one archive member: the bundle-relative path, its permission mode,
// its variant, and — for regular files — the exact content digest and size.
type Member struct {
	Path   string
	Kind   MemberKind
	Mode   uint32
	Digest artifact.Digest
	Size   int64
}

// BundleMembers returns the exact member list a bundle must produce, in the
// canonical order WriteArchive writes it. It is the expectation side of the
// round-trip check performed against every written archive.
func BundleMembers(bundle artifact.Bundle) ([]Member, error) {
	entries := bundle.Manifest().Entries()
	members := make([]Member, 0, len(entries))
	for _, entry := range entries {
		member := Member{Path: entry.Path().String(), Mode: entry.Mode().Bits()}
		switch {
		case entry.IsDirectory():
			member.Kind = MemberDirectory
		case entry.IsRegular():
			member.Kind = MemberRegularFile
			member.Digest = entry.Digest()
			content, err := readBundleFile(bundle, entry.Path().String())
			if err != nil {
				return nil, err
			}
			member.Size = int64(len(content))
		default:
			return nil, archiveFault(
				"component archive member enumeration", "every manifest entry is a directory or a regular file",
				fmt.Sprintf("manifest entry %q is neither a directory nor a regular file", entry.Path()),
				"the archive would omit or misrepresent declared bytes",
				"declare every bundle entry with artifact.NewDirectoryEntry or artifact.NewFileEntry", fs.ErrInvalid)
		}
		members = append(members, member)
	}
	sort.SliceStable(members, func(i, j int) bool { return members[i].Path < members[j].Path })
	return members, nil
}

// WriteArchive writes one bundle as a component archive: a gzip-compressed tar
// whose members are exactly the bundle manifest entries, in lexicographic path
// order, carrying the manifest's permission modes and zeroed ownership and
// timestamps. The same bundle always produces byte-identical output.
func WriteArchive(destination io.Writer, bundle artifact.Bundle) error {
	if destination == nil {
		return archiveFault(
			"component archive write", "a non-nil destination writer", "the destination writer is nil",
			"no archive bytes could be produced", "pass an open destination writer", fs.ErrInvalid)
	}
	members, err := BundleMembers(bundle)
	if err != nil {
		return err
	}
	compressor, err := gzip.NewWriterLevel(destination, gzip.BestCompression)
	if err != nil {
		return archiveFault(
			"component archive compression", "a usable gzip compression level",
			fmt.Sprintf("the gzip writer rejected the fixed compression level: %v", err),
			"no archive bytes could be produced", "report this as a Pasture defect; the level is a compile-time constant", err)
	}
	// An empty name/comment and a zero modification time keep the gzip header
	// free of run-specific bytes; OS 255 is the "unknown" system value.
	compressor.Header = gzip.Header{ModTime: time.Time{}, OS: 255}
	archive := tar.NewWriter(compressor)
	for _, member := range members {
		if err := writeMember(archive, bundle, member); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return archiveFault(
			"component archive finalization", "the tar stream closes cleanly",
			fmt.Sprintf("the tar writer failed to finish: %v", err),
			"the archive is truncated and must not be published", "retry after repairing the destination writer", err)
	}
	if err := compressor.Close(); err != nil {
		return archiveFault(
			"component archive finalization", "the gzip stream closes cleanly",
			fmt.Sprintf("the gzip writer failed to finish: %v", err),
			"the archive is truncated and must not be published", "retry after repairing the destination writer", err)
	}
	return nil
}

func writeMember(archive *tar.Writer, bundle artifact.Bundle, member Member) error {
	header := &tar.Header{
		Name:    member.Path,
		Mode:    int64(member.Mode),
		ModTime: archiveEpoch,
		Uid:     0,
		Gid:     0,
		Uname:   "",
		Gname:   "",
		Format:  tar.FormatPAX,
	}
	var content []byte
	switch member.Kind {
	case MemberDirectory:
		header.Typeflag = tar.TypeDir
		header.Name += "/"
	case MemberRegularFile:
		header.Typeflag = tar.TypeReg
		data, err := readBundleFile(bundle, member.Path)
		if err != nil {
			return err
		}
		if digest := artifact.DigestBytes(data); digest != member.Digest {
			return archiveFault(
				"component archive member digest check", "bundle bytes match their declared digest",
				fmt.Sprintf("entry %q declares %s but the bundle returned bytes digesting to %s", member.Path, member.Digest, digest),
				"the archive would publish bytes under the wrong content identity",
				"rebuild the target bundle so its manifest digests match its bytes", fs.ErrInvalid)
		}
		content = data
		header.Size = int64(len(data))
	default:
		return archiveFault(
			"component archive member write", "a known member variant",
			fmt.Sprintf("member %q has unknown variant %d", member.Path, member.Kind),
			"the archive layout would be undefined", "construct members with BundleMembers", fs.ErrInvalid)
	}
	if err := archive.WriteHeader(header); err != nil {
		return archiveFault(
			"component archive member write", "each member header is accepted by the tar writer",
			fmt.Sprintf("the header for %q was rejected: %v", member.Path, err),
			"the archive is incomplete and must not be published",
			"shorten or repair the offending bundle path and rebuild the target", err)
	}
	if len(content) > 0 {
		if _, err := archive.Write(content); err != nil {
			return archiveFault(
				"component archive member write", "each member's bytes are written completely",
				fmt.Sprintf("writing the body of %q failed: %v", member.Path, err),
				"the archive is truncated and must not be published",
				"retry after repairing the destination writer", err)
		}
	}
	return nil
}

// ReadArchive reads a component archive back into its member list, recomputing
// each regular file's digest from the archived bytes. It is the verification
// half of the format: the installer, the release pipeline, and the tests all
// use it to prove an archive matches the bundle it claims to carry.
func ReadArchive(source io.Reader) ([]Member, error) {
	if source == nil {
		return nil, archiveFault(
			"component archive read", "a non-nil source reader", "the source reader is nil",
			"no archive could be inspected", "pass an open source reader", fs.ErrInvalid)
	}
	decompressor, err := gzip.NewReader(source)
	if err != nil {
		return nil, archiveFault(
			"component archive read", "a valid gzip stream",
			fmt.Sprintf("the gzip header could not be read: %v", err),
			"the archive cannot be verified and must not be published",
			"rebuild the archive with `pasture bundle export`", err)
	}
	defer decompressor.Close()
	archive := tar.NewReader(decompressor)
	members := make([]Member, 0, 16)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, archiveFault(
				"component archive read", "a complete tar stream",
				fmt.Sprintf("the next member header could not be read: %v", err),
				"the archive cannot be verified and must not be published",
				"rebuild the archive with `pasture bundle export`", err)
		}
		member, err := memberFromHeader(header, archive)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := decompressor.Close(); err != nil {
		return nil, archiveFault(
			"component archive read", "the gzip stream ends cleanly",
			fmt.Sprintf("the gzip trailer is invalid: %v", err),
			"the archive cannot be verified and must not be published",
			"rebuild the archive with `pasture bundle export`", err)
	}
	return members, nil
}

func memberFromHeader(header *tar.Header, body io.Reader) (Member, error) {
	name := header.Name
	member := Member{Mode: uint32(header.Mode) & uint32(fs.ModePerm)}
	switch header.Typeflag {
	case tar.TypeDir:
		member.Kind = MemberDirectory
		name = trimTrailingSlash(name)
	case tar.TypeReg:
		member.Kind = MemberRegularFile
		content, err := io.ReadAll(body)
		if err != nil {
			return Member{}, archiveFault(
				"component archive read", "each member's bytes are readable",
				fmt.Sprintf("the body of %q could not be read: %v", name, err),
				"the archive cannot be verified and must not be published",
				"rebuild the archive with `pasture bundle export`", err)
		}
		member.Digest = artifact.DigestBytes(content)
		member.Size = int64(len(content))
	default:
		return Member{}, archiveFault(
			"component archive read", "only directory and regular-file members",
			fmt.Sprintf("member %q has unsupported tar type %q", name, string(header.Typeflag)),
			"symlinks, devices, and other special members cannot be installed portably",
			"rebuild the archive with `pasture bundle export`", fs.ErrInvalid)
	}
	entryPath, err := artifact.NewPath(name)
	if err != nil {
		return Member{}, archiveFault(
			"component archive read", "every member path is a canonical relative bundle path",
			fmt.Sprintf("member path %q is not canonical: %v", name, err),
			"an archived member could escape its installation root",
			"rebuild the archive with `pasture bundle export`", err)
	}
	member.Path = entryPath.String()
	return member, nil
}

func trimTrailingSlash(value string) string {
	for len(value) > 1 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}

func readBundleFile(bundle artifact.Bundle, name string) ([]byte, error) {
	file, err := bundle.Open(name)
	if err != nil {
		return nil, archiveFault(
			"component archive bundle read", "every declared regular file opens",
			fmt.Sprintf("bundle entry %q could not be opened: %v", name, err),
			"the archive would omit declared bytes",
			"rebuild the target bundle so every manifest entry is present", err)
	}
	defer file.Close()
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, file); err != nil {
		return nil, archiveFault(
			"component archive bundle read", "every declared regular file reads completely",
			fmt.Sprintf("bundle entry %q could not be read: %v", name, err),
			"the archive would carry truncated bytes",
			"rebuild the target bundle so every manifest entry reads to EOF", err)
	}
	return buffer.Bytes(), nil
}

func archiveFault(operation, rule, reason, impact, fix string, cause error) error {
	return cell.NewFault(operation, rule, reason, "internal/install/export", "exporting component archives", impact, fix, cause)
}
