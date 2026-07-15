package artifact_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
)

var (
	alpha = []byte("alpha\n")
	run   = []byte("#!/bin/sh\necho pasture\n")
)

func TestValueCodecsAndValidation(t *testing.T) {
	t.Parallel()

	entryPath := mustPath(t, "config/settings.json")
	pathText, err := entryPath.MarshalText()
	if err != nil || string(pathText) != "config/settings.json" {
		t.Fatalf("Path.MarshalText() = %q, %v", pathText, err)
	}
	var decodedPath artifact.Path
	if err := decodedPath.UnmarshalText(pathText); err != nil || decodedPath != entryPath {
		t.Fatalf("Path.UnmarshalText() = %v, %v", decodedPath, err)
	}

	mode := mustMode(t, 0o755)
	if mode.String() != "0755" || mode.Bits() != 0o755 {
		t.Fatalf("mode = %q/%#o, want 0755/0755", mode, mode.Bits())
	}
	var decodedMode artifact.Mode
	if err := decodedMode.UnmarshalText([]byte("0755")); err != nil || decodedMode != mode {
		t.Fatalf("Mode.UnmarshalText() = %v, %v", decodedMode, err)
	}
	if zeroMode := mustMode(t, 0); zeroMode.String() != "0000" {
		t.Fatalf("zero mode = %q, want 0000", zeroMode)
	}

	digest := artifact.DigestBytes([]byte("hello"))
	const wantDigest = "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if digest.String() != wantDigest {
		t.Fatalf("DigestBytes(hello) = %q, want %q", digest, wantDigest)
	}
	parsedDigest, err := artifact.ParseDigest(wantDigest)
	if err != nil || parsedDigest != digest {
		t.Fatalf("ParseDigest() = %v, %v", parsedDigest, err)
	}
	digestText, err := digest.MarshalText()
	if err != nil {
		t.Fatalf("Digest.MarshalText(): %v", err)
	}
	var decodedDigest artifact.Digest
	if err := decodedDigest.UnmarshalText(digestText); err != nil || decodedDigest != digest {
		t.Fatalf("Digest.UnmarshalText() = %v, %v", decodedDigest, err)
	}

	for _, kind := range []artifact.EntryType{artifact.RegularFileType(), artifact.DirectoryType()} {
		text, err := kind.MarshalText()
		if err != nil {
			t.Fatalf("%v.MarshalText(): %v", kind, err)
		}
		var decoded artifact.EntryType
		if err := decoded.UnmarshalText(text); err != nil || decoded != kind {
			t.Fatalf("EntryType.UnmarshalText(%q) = %v, %v", text, decoded, err)
		}
	}

	validID := "artifact.bundle.v1:sha256:" + strings.Repeat("a", 64)
	id, err := artifact.ParseBundleID(validID)
	if err != nil || id.String() != validID {
		t.Fatalf("ParseBundleID() = %v, %v", id, err)
	}
	encodedID, err := json.Marshal(id)
	if err != nil || string(encodedID) != `"`+validID+`"` {
		t.Fatalf("json.Marshal(BundleID) = %s, %v", encodedID, err)
	}
	var decodedID artifact.BundleID
	if err := json.Unmarshal(encodedID, &decodedID); err != nil || decodedID != id {
		t.Fatalf("json.Unmarshal(BundleID) = %v, %v", decodedID, err)
	}
}

func TestInvalidScalarValuesReject(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", ".", "../escape", "/absolute", "a/../b", "a//b", "a/", `a\b`, "a\x00b"} {
		value := value
		t.Run("path_"+strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			t.Parallel()
			if _, err := artifact.NewPath(value); err == nil {
				t.Fatalf("NewPath(%q) succeeded, want error", value)
			}
		})
	}

	for _, value := range []string{"", "644", "06440", "08ff", "0x44"} {
		value := value
		t.Run("mode_"+value, func(t *testing.T) {
			t.Parallel()
			if _, err := artifact.ParseMode(value); err == nil {
				t.Fatalf("ParseMode(%q) succeeded, want error", value)
			}
		})
	}
	if _, err := artifact.NewMode(0o1000); err == nil {
		t.Fatal("NewMode(01000) succeeded, want error")
	}

	for _, value := range []string{
		"", "sha256:", "sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("A", 64), "md5:" + strings.Repeat("a", 64),
	} {
		if _, err := artifact.ParseDigest(value); err == nil {
			t.Errorf("ParseDigest(%q) succeeded, want error", value)
		}
	}
	for _, value := range []string{
		"", "artifact.bundle.v1:sha256:",
		"artifact.bundle.v1:sha256:" + strings.Repeat("a", 63),
		"artifact.bundle.v1:sha256:" + strings.Repeat("A", 64),
		"artifact.bundle.v2:sha256:" + strings.Repeat("a", 64),
	} {
		if _, err := artifact.ParseBundleID(value); err == nil {
			t.Errorf("ParseBundleID(%q) succeeded, want error", value)
		}
	}
	if _, err := artifact.ParseEntryType("symlink"); err == nil {
		t.Fatal("ParseEntryType(symlink) succeeded, want error")
	}
}

func TestManifestCanonicalCodecAndOwnership(t *testing.T) {
	t.Parallel()

	directory := mustDirectoryEntry(t, "config", 0o755)
	settings := mustFileEntry(t, "config/settings.json", 0o644, alpha)
	launcher := mustFileEntry(t, "run.sh", 0o755, run)
	manifest := mustManifest(t, launcher, settings, directory)

	entries := manifest.Entries()
	wantPaths := []string{"config", "config/settings.json", "run.sh"}
	if len(entries) != len(wantPaths) {
		t.Fatalf("manifest entries = %d, want %d", len(entries), len(wantPaths))
	}
	for i, want := range wantPaths {
		if got := entries[i].Path().String(); got != want {
			t.Errorf("entry[%d].Path = %q, want %q", i, got, want)
		}
	}
	if !entries[0].IsDirectory() || entries[0].Digest().String() != "" {
		t.Fatalf("directory entry = %#v, want directory without digest", entries[0])
	}
	if !entries[1].IsRegular() || entries[1].Mode().String() != "0644" || entries[1].Digest() != artifact.DigestBytes(alpha) {
		t.Fatalf("settings entry did not round-trip type/mode/digest: %#v", entries[1])
	}

	encoded, err := manifest.MarshalBinary()
	if err != nil {
		t.Fatalf("Manifest.MarshalBinary(): %v", err)
	}
	wantJSON := `{"schema":"artifact.manifest.v1","entries":[` +
		`{"path":"config","type":"directory","mode":"0755"},` +
		`{"path":"config/settings.json","type":"regular-file","mode":"0644","digest":"` + artifact.DigestBytes(alpha).String() + `"},` +
		`{"path":"run.sh","type":"regular-file","mode":"0755","digest":"` + artifact.DigestBytes(run).String() + `"}]}`
	if string(encoded) != wantJSON {
		t.Fatalf("manifest JSON:\n got: %s\nwant: %s", encoded, wantJSON)
	}

	decoded, err := artifact.ParseManifest(encoded)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	if !decoded.Equal(manifest) {
		t.Fatal("decoded manifest is not equal to source manifest")
	}
	reencoded, err := json.Marshal(decoded)
	if err != nil || string(reencoded) != wantJSON {
		t.Fatalf("reencoded manifest = %s, %v", reencoded, err)
	}

	// Reordering an input codec does not affect the validated value or encoding.
	unsorted := `{"schema":"artifact.manifest.v1","entries":[` +
		`{"path":"run.sh","type":"regular-file","mode":"0755","digest":"` + artifact.DigestBytes(run).String() + `"},` +
		`{"path":"config/settings.json","type":"regular-file","mode":"0644","digest":"` + artifact.DigestBytes(alpha).String() + `"},` +
		`{"path":"config","type":"directory","mode":"0755"}]}`
	fromUnsorted, err := artifact.ParseManifest([]byte(unsorted))
	if err != nil || !fromUnsorted.Equal(manifest) {
		t.Fatalf("ParseManifest(unsorted) = %#v, %v", fromUnsorted, err)
	}
	canonical, err := fromUnsorted.MarshalJSON()
	if err != nil || string(canonical) != wantJSON {
		t.Fatalf("canonical unsorted manifest = %s, %v", canonical, err)
	}

	// Mutating the returned slice never changes the manifest.
	entries[0] = launcher
	if got := manifest.Entries()[0].Path().String(); got != "config" {
		t.Fatalf("manifest changed through Entries alias: first path = %q", got)
	}
}

func TestManifestRejectsInvalidEntriesAndCodecStates(t *testing.T) {
	t.Parallel()

	file := mustFileEntry(t, "a", 0o644, alpha)
	descendant := mustFileEntry(t, "a/b", 0o644, alpha)
	if _, err := artifact.NewManifest(file, file); err == nil {
		t.Fatal("duplicate manifest path succeeded, want error")
	}
	if _, err := artifact.NewManifest(file, descendant); err == nil {
		t.Fatal("parent-file conflict succeeded, want error")
	}
	if _, err := artifact.NewManifest(artifact.Entry{}); err == nil {
		t.Fatal("zero entry succeeded, want error")
	}
	if _, err := artifact.NewFileEntry(artifact.Path{}, mustMode(t, 0o644), artifact.DigestBytes(alpha)); err == nil {
		t.Fatal("zero path file entry succeeded, want error")
	}
	if _, err := artifact.NewFileEntry(mustPath(t, "a"), artifact.Mode{}, artifact.DigestBytes(alpha)); err == nil {
		t.Fatal("zero mode file entry succeeded, want error")
	}
	if _, err := artifact.NewFileEntry(mustPath(t, "a"), mustMode(t, 0o644), artifact.Digest{}); err == nil {
		t.Fatal("zero digest file entry succeeded, want error")
	}

	invalidCodecs := []string{
		``,
		`{"schema":"artifact.manifest.v1"}`,
		`{"schema":"artifact.manifest.v1","entries":null}`,
		`{"schema":"artifact.manifest.v2","entries":[]}`,
		`{"schema":"artifact.manifest.v1","entries":[],"extra":true}`,
		`{"schema":"artifact.manifest.v1","entries":[]} {}`,
		`{"schema":"artifact.manifest.v1","entries":[{"path":"../a","type":"regular-file","mode":"0644","digest":"` + artifact.DigestBytes(alpha).String() + `"}]}`,
		`{"schema":"artifact.manifest.v1","entries":[{"path":"a","type":"symlink","mode":"0644"}]}`,
		`{"schema":"artifact.manifest.v1","entries":[{"path":"a","type":"regular-file","mode":"644","digest":"` + artifact.DigestBytes(alpha).String() + `"}]}`,
		`{"schema":"artifact.manifest.v1","entries":[{"path":"a","type":"regular-file","mode":"0644"}]}`,
		`{"schema":"artifact.manifest.v1","entries":[{"path":"a","type":"directory","mode":"0755","digest":"` + artifact.DigestBytes(nil).String() + `"}]}`,
	}
	for i, encoded := range invalidCodecs {
		if _, err := artifact.ParseManifest([]byte(encoded)); err == nil {
			t.Errorf("ParseManifest invalid case %d succeeded: %s", i, encoded)
		}
	}
}

func TestBundleSnapshotsSourceAndReturnsFreshReadOnlyFiles(t *testing.T) {
	t.Parallel()

	source := fixtureFS()
	manifest := fixtureManifest(t)
	bundle, err := artifact.NewBundle(source, manifest)
	if err != nil {
		t.Fatalf("NewBundle(): %v", err)
	}
	const wantBundleID = "artifact.bundle.v1:sha256:c5b6aa03a9f9961720b1760fb5f91b218fb123d6ef45f3bc12fe477d98c54441"
	if got := bundle.ID().String(); got != wantBundleID {
		t.Fatalf("Bundle.ID() = %q, want canonical golden %q", got, wantBundleID)
	}
	if _, err := artifact.ParseBundleID(bundle.ID().String()); err != nil {
		t.Fatalf("derived BundleID invalid: %v", err)
	}
	if !bundle.Manifest().Equal(manifest) {
		t.Fatal("Bundle.Manifest differs from construction manifest")
	}
	equivalent, err := artifact.NewBundle(fixtureFS(), manifest)
	if err != nil {
		t.Fatalf("NewBundle(equivalent): %v", err)
	}

	// Replace and mutate the custom backing filesystem after construction.
	source["config/settings.json"].Data[0] = 'X'
	source["run.sh"] = &fstest.MapFile{Data: []byte("replaced"), Mode: 0o600}
	if got := readBundleFile(t, bundle, "config/settings.json"); string(got) != string(alpha) {
		t.Fatalf("snapshot changed with source mutation: %q", got)
	}
	if got := readBundleFile(t, bundle, "run.sh"); string(got) != string(run) {
		t.Fatalf("snapshot changed with source replacement: %q", got)
	}
	if bundle.ID().String() != wantBundleID || !bundle.Manifest().Equal(manifest) || !bundle.Equal(equivalent) {
		t.Fatal("source mutation changed bundle ID, manifest, or equality")
	}

	first, err := bundle.Open("config/settings.json")
	if err != nil {
		t.Fatalf("first Open(): %v", err)
	}
	second, err := bundle.Open("config/settings.json")
	if err != nil {
		t.Fatalf("second Open(): %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	t.Cleanup(func() { _ = second.Close() })
	if _, writable := first.(io.Writer); writable {
		t.Fatal("Bundle.Open returned an io.Writer")
	}
	one := make([]byte, 1)
	if _, err := first.Read(one); err != nil || string(one) != "a" {
		t.Fatalf("first.Read() = %q, %v", one, err)
	}
	allSecond, err := io.ReadAll(second)
	if err != nil || string(allSecond) != string(alpha) {
		t.Fatalf("second handle did not start fresh: %q, %v", allSecond, err)
	}
	info, err := first.Stat()
	if err != nil || info.Mode().Perm() != 0o644 || info.Size() != int64(len(alpha)) || info.IsDir() {
		t.Fatalf("Stat() = %#v, %v", info, err)
	}
}

func TestBundleConcurrentReadsAreIsolated(t *testing.T) {
	t.Parallel()

	manifest := fixtureManifest(t)
	bundle, err := artifact.NewBundle(fixtureFS(), manifest)
	if err != nil {
		t.Fatalf("NewBundle(): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 32)
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if ctx.Err() != nil {
				return
			}
			file, openErr := bundle.Open("run.sh")
			if openErr != nil {
				errCh <- openErr
				cancel()
				return
			}
			content, readErr := io.ReadAll(file)
			closeErr := file.Close()
			if readErr != nil || closeErr != nil || string(content) != string(run) {
				errCh <- errors.Join(readErr, closeErr, errors.New("concurrent read bytes differ"))
				cancel()
				return
			}
			if bundle.ID().String() == "" || !bundle.Manifest().Equal(manifest) {
				errCh <- errors.New("concurrent identity or manifest read failed")
				cancel()
			}
		}()
	}
	workers.Wait()
	close(errCh)
	for workerErr := range errCh {
		if workerErr != nil {
			t.Fatalf("concurrent bundle read: %v", workerErr)
		}
	}
}

func TestBundleIgnoresEnumerationOrderAndWritableSourceCapability(t *testing.T) {
	t.Parallel()

	manifest := fixtureManifest(t)
	ordered, err := artifact.NewBundle(fixtureFS(), manifest)
	if err != nil {
		t.Fatalf("NewBundle(ordered): %v", err)
	}
	reversed, err := artifact.NewBundle(reverseFS{MapFS: fixtureFS()}, manifest)
	if err != nil {
		t.Fatalf("NewBundle(reversed): %v", err)
	}
	if ordered.ID() != reversed.ID() || !ordered.Equal(reversed) {
		t.Fatalf("enumeration order changed identity: %s != %s", ordered.ID(), reversed.ID())
	}
	orderedJSON, err := ordered.Manifest().MarshalJSON()
	if err != nil {
		t.Fatalf("ordered manifest: %v", err)
	}
	reversedJSON, err := reversed.Manifest().MarshalJSON()
	if err != nil || string(orderedJSON) != string(reversedJSON) {
		t.Fatalf("manifest encoding changed with enumeration: %s != %s, %v", orderedJSON, reversedJSON, err)
	}

	malicious, err := artifact.NewBundle(writableFS{MapFS: fixtureFS()}, manifest)
	if err != nil {
		t.Fatalf("NewBundle(writable source): %v", err)
	}
	handle, err := malicious.Open("run.sh")
	if err != nil {
		t.Fatalf("malicious bundle Open(): %v", err)
	}
	defer handle.Close()
	if _, writable := handle.(io.Writer); writable {
		t.Fatal("writable source capability escaped through Bundle.Open")
	}
}

func TestBundleRejectsSourceBoundaryViolations(t *testing.T) {
	t.Parallel()

	manifest := fixtureManifest(t)
	tests := []struct {
		name   string
		source fs.FS
	}{
		{
			name: "missing declared file",
			source: fstest.MapFS{
				"config":               &fstest.MapFile{Mode: fs.ModeDir | 0o755},
				"config/settings.json": &fstest.MapFile{Data: append([]byte(nil), alpha...), Mode: 0o644},
			},
		},
		{
			name: "extra file",
			source: func() fstest.MapFS {
				value := fixtureFS()
				value["extra.txt"] = &fstest.MapFile{Data: []byte("extra")}
				return value
			}(),
		},
		{
			name: "extra empty directory",
			source: func() fstest.MapFS {
				value := fixtureFS()
				value["empty"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
				return value
			}(),
		},
		{
			name: "unsupported symlink",
			source: func() fstest.MapFS {
				value := fixtureFS()
				value["run.sh"] = &fstest.MapFile{Data: []byte("config/settings.json"), Mode: fs.ModeSymlink | 0o777}
				return value
			}(),
		},
		{
			name: "directory where file declared",
			source: func() fstest.MapFS {
				value := fixtureFS()
				value["run.sh"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
				return value
			}(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := artifact.NewBundle(test.source, manifest); err == nil {
				t.Fatal("NewBundle succeeded, want error")
			}
		})
	}

	changed := fixtureFS()
	changed["run.sh"].Data = []byte("changed")
	_, err := artifact.NewBundle(changed, manifest)
	assertActionableError(t, err, "run.sh")
	changedAgain := fixtureFS()
	changedAgain["run.sh"].Data = []byte("changed")
	_, repeatedErr := artifact.NewBundle(changedAgain, manifest)
	if repeatedErr == nil || repeatedErr.Error() != err.Error() {
		t.Fatalf("same validation failure was not deterministic:\nfirst:  %v\nsecond: %v", err, repeatedErr)
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("digest mismatch error does not wrap fs.ErrInvalid: %v", err)
	}
	if _, err := artifact.NewBundle(nil, manifest); err == nil {
		t.Fatal("NewBundle(nil) succeeded, want error")
	}
	if _, err := artifact.NewBundle(fixtureFS(), artifact.Manifest{}); err == nil {
		t.Fatal("NewBundle(zero manifest) succeeded, want error")
	}
}

func TestBundleIdentityChangesWithCanonicalManifestContent(t *testing.T) {
	t.Parallel()

	baseSource := fixtureFS()
	base, err := artifact.NewBundle(baseSource, fixtureManifest(t))
	if err != nil {
		t.Fatalf("NewBundle(base): %v", err)
	}

	modeManifest := mustManifest(t,
		mustDirectoryEntry(t, "config", 0o755),
		mustFileEntry(t, "config/settings.json", 0o600, alpha),
		mustFileEntry(t, "run.sh", 0o755, run),
	)
	modeBundle, err := artifact.NewBundle(fixtureFS(), modeManifest)
	if err != nil {
		t.Fatalf("NewBundle(mode): %v", err)
	}
	assertDifferentID(t, base, modeBundle, "mode")

	contentSource := fixtureFS()
	changedContent := []byte("beta\n")
	contentSource["config/settings.json"].Data = changedContent
	contentManifest := mustManifest(t,
		mustDirectoryEntry(t, "config", 0o755),
		mustFileEntry(t, "config/settings.json", 0o644, changedContent),
		mustFileEntry(t, "run.sh", 0o755, run),
	)
	contentBundle, err := artifact.NewBundle(contentSource, contentManifest)
	if err != nil {
		t.Fatalf("NewBundle(content): %v", err)
	}
	assertDifferentID(t, base, contentBundle, "content")

	pathSource := fstest.MapFS{
		"config":        &fstest.MapFile{Mode: fs.ModeDir | 0o755},
		"config/a.json": &fstest.MapFile{Data: append([]byte(nil), alpha...), Mode: 0o644},
		"run.sh":        &fstest.MapFile{Data: append([]byte(nil), run...), Mode: 0o755},
	}
	pathManifest := mustManifest(t,
		mustDirectoryEntry(t, "config", 0o755),
		mustFileEntry(t, "config/a.json", 0o644, alpha),
		mustFileEntry(t, "run.sh", 0o755, run),
	)
	pathBundle, err := artifact.NewBundle(pathSource, pathManifest)
	if err != nil {
		t.Fatalf("NewBundle(path): %v", err)
	}
	assertDifferentID(t, base, pathBundle, "path")

	entrySetSource := fixtureFS()
	entrySetSource["extra.txt"] = &fstest.MapFile{Data: []byte("extra"), Mode: 0o644}
	entrySetManifest := mustManifest(t,
		mustDirectoryEntry(t, "config", 0o755),
		mustFileEntry(t, "config/settings.json", 0o644, alpha),
		mustFileEntry(t, "extra.txt", 0o644, []byte("extra")),
		mustFileEntry(t, "run.sh", 0o755, run),
	)
	entrySetBundle, err := artifact.NewBundle(entrySetSource, entrySetManifest)
	if err != nil {
		t.Fatalf("NewBundle(entry set): %v", err)
	}
	assertDifferentID(t, base, entrySetBundle, "entry set")
}

func TestBundleOpenRejectsDirectoriesUndeclaredAndUncleanPaths(t *testing.T) {
	t.Parallel()

	bundle, err := artifact.NewBundle(fixtureFS(), fixtureManifest(t))
	if err != nil {
		t.Fatalf("NewBundle(): %v", err)
	}
	tests := []struct {
		name   string
		path   string
		isKind error
	}{
		{name: "directory", path: "config", isKind: fs.ErrNotExist},
		{name: "undeclared", path: "missing", isKind: fs.ErrNotExist},
		{name: "traversal", path: "../run.sh", isKind: fs.ErrInvalid},
		{name: "absolute", path: "/run.sh", isKind: fs.ErrInvalid},
		{name: "unclean", path: "config//settings.json", isKind: fs.ErrInvalid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := bundle.Open(test.path)
			if err == nil {
				t.Fatal("Open succeeded, want error")
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != test.path {
				t.Fatalf("Open error = %T %v, want PathError for %q", err, err, test.path)
			}
			if !errors.Is(err, test.isKind) {
				t.Fatalf("Open error = %v, want errors.Is(%v)", err, test.isKind)
			}
			assertActionableError(t, err, test.path)
		})
	}
	if _, err := (artifact.Bundle{}).Open("run.sh"); err == nil {
		t.Fatal("zero Bundle.Open succeeded, want error")
	}
}

func fixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"config":               &fstest.MapFile{Mode: fs.ModeDir | 0o755},
		"config/settings.json": &fstest.MapFile{Data: append([]byte(nil), alpha...), Mode: 0o644},
		"run.sh":               &fstest.MapFile{Data: append([]byte(nil), run...), Mode: 0o755},
	}
}

func fixtureManifest(t *testing.T) artifact.Manifest {
	t.Helper()
	return mustManifest(t,
		mustDirectoryEntry(t, "config", 0o755),
		mustFileEntry(t, "config/settings.json", 0o644, alpha),
		mustFileEntry(t, "run.sh", 0o755, run),
	)
}

func mustPath(t *testing.T, value string) artifact.Path {
	t.Helper()
	result, err := artifact.NewPath(value)
	if err != nil {
		t.Fatalf("NewPath(%q): %v", value, err)
	}
	return result
}

func mustMode(t *testing.T, value uint32) artifact.Mode {
	t.Helper()
	result, err := artifact.NewMode(value)
	if err != nil {
		t.Fatalf("NewMode(%#o): %v", value, err)
	}
	return result
}

func mustFileEntry(t *testing.T, name string, mode uint32, content []byte) artifact.Entry {
	t.Helper()
	result, err := artifact.NewFileEntry(mustPath(t, name), mustMode(t, mode), artifact.DigestBytes(content))
	if err != nil {
		t.Fatalf("NewFileEntry(%q): %v", name, err)
	}
	return result
}

func mustDirectoryEntry(t *testing.T, name string, mode uint32) artifact.Entry {
	t.Helper()
	result, err := artifact.NewDirectoryEntry(mustPath(t, name), mustMode(t, mode))
	if err != nil {
		t.Fatalf("NewDirectoryEntry(%q): %v", name, err)
	}
	return result
}

func mustManifest(t *testing.T, entries ...artifact.Entry) artifact.Manifest {
	t.Helper()
	result, err := artifact.NewManifest(entries...)
	if err != nil {
		t.Fatalf("NewManifest(): %v", err)
	}
	return result
}

func readBundleFile(t *testing.T, bundle artifact.Bundle, name string) []byte {
	t.Helper()
	file, err := bundle.Open(name)
	if err != nil {
		t.Fatalf("Bundle.Open(%q): %v", name, err)
	}
	defer file.Close()
	result, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q): %v", name, err)
	}
	return result
}

func assertDifferentID(t *testing.T, left, right artifact.Bundle, dimension string) {
	t.Helper()
	if left.ID() == right.ID() || left.Equal(right) {
		t.Fatalf("changing %s did not change bundle identity: %s", dimension, left.ID())
	}
}

func assertActionableError(t *testing.T, err error, entry string) {
	t.Helper()
	if err == nil {
		t.Fatal("error is nil")
	}
	var validation *artifact.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error %T does not contain *artifact.ValidationError: %v", err, err)
	}
	if validation.Stage == "" || validation.Bundle == "" || validation.Entry == "" ||
		validation.Rule == "" || validation.Reason == "" || validation.Impact == "" || validation.Fix == "" {
		t.Fatalf("validation error is not fully actionable: %#v", validation)
	}
	if entry != "" && validation.Entry != entry {
		t.Fatalf("validation entry = %q, want %q", validation.Entry, entry)
	}
}

type reverseFS struct{ fstest.MapFS }

func (r reverseFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(r.MapFS, name)
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries, nil
}

type writableFS struct{ fstest.MapFS }

func (w writableFS) Open(name string) (fs.File, error) {
	file, err := w.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if info.Mode().IsRegular() {
		return &writableSourceFile{File: file}, nil
	}
	return file, nil
}

type writableSourceFile struct{ fs.File }

func (f *writableSourceFile) Write(content []byte) (int, error) { return len(content), nil }
