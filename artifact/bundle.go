package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sync"
	"time"
)

const bundleIdentityDomain = "artifact.bundle.v1\x00"

// Bundle is an immutable content-addressed artifact and implements fs.FS for
// its declared regular-file leaves.
type Bundle struct {
	id       BundleID
	manifest Manifest
	entries  map[string]Entry
	files    map[string][]byte
	valid    bool
}

// NewBundle validates source against manifest and snapshots every file byte.
// The returned value owns no handle or alias into source.
func NewBundle(source fs.FS, manifest Manifest) (Bundle, error) {
	if source == nil {
		return Bundle{}, invalid(
			"bundle source snapshot", "<pending>", "", "non-nil source filesystem",
			"the source fs.FS is nil", "no artifact bytes can be validated or captured",
			"provide an fs.FS containing exactly the manifest's files and directories", fs.ErrInvalid,
		)
	}
	if !manifest.valid {
		return Bundle{}, invalid(
			"bundle source snapshot", "<pending>", "", "validated manifest",
			"the manifest was not constructed", "the source has no trustworthy ownership inventory",
			"construct the manifest with artifact.NewManifest or artifact.ParseManifest", fs.ErrInvalid,
		)
	}

	ownedManifest := manifest.clone()
	declared := ownedManifest.entryMap()
	seen := make(map[string]struct{}, len(declared))
	snapshot := make(map[string][]byte, len(declared))

	err := fs.WalkDir(source, ".", func(name string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return invalid(
				"bundle source walk", "<pending>", name, "readable source tree",
				fmt.Sprintf("the source filesystem could not enumerate this location: %v", walkErr),
				"the complete artifact inventory cannot be verified",
				"repair the source filesystem so every declared entry and parent directory can be read", walkErr,
			)
		}
		if name == "." {
			if !dirEntry.IsDir() {
				return invalid(
					"bundle source walk", "<pending>", name, "directory bundle root",
					"the fs.FS root is not a directory", "artifact leaves cannot be enumerated safely",
					"provide an fs.FS whose '.' root is a directory", fs.ErrInvalid,
				)
			}
			return nil
		}

		entryPath, err := NewPath(name)
		if err != nil {
			return invalid(
				"bundle source walk", "<pending>", name, "canonical source path",
				"the source enumerated a path that is not a canonical artifact path",
				"the source tree could escape or collide at an installation boundary",
				"make every source path a clean slash-separated relative path", err,
			)
		}
		name = entryPath.String()
		if _, duplicate := seen[name]; duplicate {
			return invalid(
				"bundle source walk", "<pending>", name, "unique source path",
				"the source enumerated the same path more than once",
				"source ownership is ambiguous and cannot be snapshotted deterministically",
				"repair the fs.FS implementation so each path is enumerated exactly once", fs.ErrExist,
			)
		}

		info, err := dirEntry.Info()
		if err != nil {
			return invalid(
				"bundle source inspection", "<pending>", name, "readable source metadata",
				fmt.Sprintf("the source entry metadata could not be read: %v", err),
				"entry type cannot be checked against the manifest",
				"repair the source fs.FS so DirEntry.Info succeeds", err,
			)
		}
		if dirEntry.IsDir() != info.IsDir() {
			return invalid(
				"bundle source inspection", "<pending>", name, "consistent source metadata",
				fmt.Sprintf("DirEntry directory status disagrees with FileInfo mode %s", info.Mode().Type()),
				"a changing or malicious source could be interpreted differently across reads",
				"provide a stable fs.FS whose DirEntry and FileInfo report the same type", fs.ErrInvalid,
			)
		}

		declaredEntry, isDeclared := declared[name]
		switch {
		case info.IsDir():
			if !ownedManifest.hasPathOrDescendant(name) {
				return invalid(
					"bundle source ownership check", "<pending>", name, "no extra source directory",
					"the source contains a directory that is neither declared nor a parent of a declared entry",
					"the source and manifest entry sets differ",
					"remove the extra directory or add a matching directory entry to the manifest", fs.ErrExist,
				)
			}
			if isDeclared {
				if !declaredEntry.IsDirectory() {
					return sourceTypeMismatch(name, declaredEntry, "directory")
				}
				seen[name] = struct{}{}
			}
			return nil
		case info.Mode().IsRegular():
			if !isDeclared {
				return invalid(
					"bundle source ownership check", "<pending>", name, "no extra source file",
					"the source contains a regular file absent from the manifest",
					"undeclared bytes would bypass content identity and ownership checks",
					"remove the extra file or add a matching regular-file entry and digest to the manifest", fs.ErrExist,
				)
			}
			if !declaredEntry.IsRegular() {
				return sourceTypeMismatch(name, declaredEntry, "regular-file")
			}
			content, readErr := readAndCloseSource(source, name)
			if readErr != nil {
				return readErr
			}
			actualDigest := DigestBytes(content)
			if actualDigest != declaredEntry.digest {
				return invalid(
					"bundle content validation", "<pending>", name, "declared content digest matches exact bytes",
					fmt.Sprintf("manifest declares %s but source bytes digest to %s", declaredEntry.digest, actualDigest),
					"the bundle would publish bytes under the wrong content identity",
					fmt.Sprintf("update the source bytes or declare digest %s", actualDigest), fs.ErrInvalid,
				)
			}
			snapshot[name] = bytes.Clone(content)
			seen[name] = struct{}{}
			return nil
		default:
			return invalid(
				"bundle source inspection", "<pending>", name, "supported source entry type",
				fmt.Sprintf("source mode %s is neither a directory nor a regular file", info.Mode()),
				"symlinks, devices, sockets, and other special entries cannot be snapshotted portably",
				"replace the entry with a declared regular file or directory", fs.ErrInvalid,
			)
		}
	})
	if err != nil {
		return Bundle{}, err
	}

	for _, entry := range ownedManifest.entries {
		if _, ok := seen[entry.path.value]; !ok {
			return Bundle{}, invalid(
				"bundle source ownership check", "<pending>", entry.path.value, "every declared entry exists",
				"the manifest entry is missing from the source filesystem",
				"the bundle would be incomplete relative to its ownership inventory",
				"add the matching source entry or remove it from the manifest", fs.ErrNotExist,
			)
		}
	}

	id := deriveBundleID(ownedManifest)
	return Bundle{id: id, manifest: ownedManifest, entries: declared, files: snapshot, valid: true}, nil
}

func sourceTypeMismatch(name string, declared Entry, actual string) error {
	return invalid(
		"bundle source type validation", "<pending>", name, "source type matches manifest",
		fmt.Sprintf("manifest declares %s but source is %s", declared.kind, actual),
		"the same path would have conflicting ownership semantics",
		fmt.Sprintf("change the source entry to %s or declare its actual type", declared.kind), fs.ErrInvalid,
	)
}

func readAndCloseSource(source fs.FS, name string) ([]byte, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, invalid(
			"bundle source read", "<pending>", name, "open declared regular file",
			fmt.Sprintf("the source file could not be opened: %v", err),
			"its exact bytes cannot be validated or snapshotted",
			"repair the fs.FS so the declared file opens for reading", err,
		)
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, invalid(
			"bundle source read", "<pending>", name, "read complete declared file",
			fmt.Sprintf("the source file could not be read completely: %v", readErr),
			"its exact bytes cannot be validated or snapshotted",
			"repair the fs.FS so reading reaches a clean EOF", readErr,
		)
	}
	if closeErr != nil {
		return nil, invalid(
			"bundle source close", "<pending>", name, "close source file after snapshot",
			fmt.Sprintf("the source file reported a close failure: %v", closeErr),
			"source resource ownership cannot be completed reliably",
			"repair the fs.File implementation so Close succeeds", closeErr,
		)
	}
	return content, nil
}

func deriveBundleID(manifest Manifest) BundleID {
	hash := sha256.New()
	_, _ = hash.Write([]byte(bundleIdentityDomain))
	writeUint64(hash, uint64(len(manifest.entries)))
	for _, entry := range manifest.entries {
		writeIdentityField(hash, entry.path.value)
		writeIdentityField(hash, entry.kind.name)
		writeUint32(hash, entry.mode.bits)
		writeIdentityField(hash, entry.digest.value)
	}
	return BundleID{value: bundleIDPrefix + hex.EncodeToString(hash.Sum(nil))}
}

func writeIdentityField(writer io.Writer, value string) {
	writeUint64(writer, uint64(len(value)))
	_, _ = io.WriteString(writer, value)
}

func writeUint64(writer io.Writer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func writeUint32(writer io.Writer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

// ID returns the bundle's derived content address.
func (b Bundle) ID() BundleID { return b.id }

// Manifest returns an owned immutable copy of the bundle inventory.
func (b Bundle) Manifest() Manifest { return b.manifest.clone() }

// Equal reports whether two validated bundles have identical canonical
// manifests and content addresses.
func (b Bundle) Equal(other Bundle) bool {
	return b.valid && other.valid && b.id == other.id && b.manifest.Equal(other.manifest)
}

// Open returns a fresh read-only handle for a declared regular-file leaf.
func (b Bundle) Open(name string) (fs.File, error) {
	entryPath, err := NewPath(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: invalid(
			"bundle open", b.id.String(), name, "declared canonical regular-file path",
			"the requested path is not canonical", "no bundle bytes were exposed",
			"open one exact regular-file path returned by Bundle.Manifest", err,
		)}
	}
	if !b.valid {
		return nil, &fs.PathError{Op: "open", Path: name, Err: invalid(
			"bundle open", "<unidentified>", entryPath.String(), "validated bundle",
			"the zero Bundle value was not constructed", "no bundle bytes were exposed",
			"construct the bundle with artifact.NewBundle", fs.ErrInvalid,
		)}
	}
	entry, declared := b.entries[entryPath.String()]
	if !declared {
		return nil, &fs.PathError{Op: "open", Path: name, Err: invalid(
			"bundle open", b.id.String(), entryPath.String(), "declared regular-file leaf",
			"the path is absent from the manifest", "undeclared bytes cannot be opened",
			"open one exact regular-file path returned by Bundle.Manifest", fs.ErrNotExist,
		)}
	}
	if !entry.IsRegular() {
		return nil, &fs.PathError{Op: "open", Path: name, Err: invalid(
			"bundle open", b.id.String(), entryPath.String(), "regular-file leaf",
			"the declared entry is a directory", "directory handles are outside the bundle read boundary",
			"open a declared regular-file entry instead", fs.ErrNotExist,
		)}
	}
	content, captured := b.files[entryPath.String()]
	if !captured {
		return nil, &fs.PathError{Op: "open", Path: name, Err: invalid(
			"bundle open", b.id.String(), entryPath.String(), "captured file bytes",
			"the validated bundle is missing its private byte snapshot", "the file cannot be opened safely",
			"reconstruct the bundle from its source and manifest", fs.ErrInvalid,
		)}
	}
	return &readOnlyFile{
		reader: bytes.NewReader(content),
		info: readOnlyFileInfo{
			name: path.Base(entryPath.String()),
			size: int64(len(content)),
			mode: fs.FileMode(entry.mode.bits),
		},
	}, nil
}

type readOnlyFile struct {
	mu     sync.Mutex
	reader *bytes.Reader
	info   readOnlyFileInfo
	closed bool
}

func (f *readOnlyFile) Read(destination []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.reader.Read(destination)
}

func (f *readOnlyFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.reader.Seek(offset, whence)
}

func (f *readOnlyFile) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fs.ErrClosed
	}
	return f.info, nil
}

func (f *readOnlyFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fs.ErrClosed
	}
	f.closed = true
	return nil
}

type readOnlyFileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (i readOnlyFileInfo) Name() string       { return i.name }
func (i readOnlyFileInfo) Size() int64        { return i.size }
func (i readOnlyFileInfo) Mode() fs.FileMode  { return i.mode }
func (i readOnlyFileInfo) ModTime() time.Time { return time.Time{} }
func (i readOnlyFileInfo) IsDir() bool        { return false }
func (i readOnlyFileInfo) Sys() any           { return nil }
