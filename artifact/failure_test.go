package artifact_test

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
)

func TestBundleConstructionFailuresCarryCandidateIDAndExactStructure(t *testing.T) {
	t.Parallel()

	manifest := fixtureManifest(t)
	candidateID := fixtureCandidateID(t, manifest)
	changedDigest := artifact.DigestBytes([]byte("changed"))
	tests := []struct {
		name   string
		source func() fs.FS
		want   validationExpectation
	}{
		{
			name: "digest mismatch",
			source: func() fs.FS {
				value := fixtureFS()
				value["run.sh"].Data = []byte("changed")
				return value
			},
			want: validationExpectation{
				stage:  "bundle content validation",
				entry:  "run.sh",
				rule:   "declared content digest matches exact bytes",
				reason: fmt.Sprintf("manifest declares %s but source bytes digest to %s", artifact.DigestBytes(run), changedDigest),
				impact: "the bundle would publish bytes under the wrong content identity",
				fix:    fmt.Sprintf("update the source bytes or declare digest %s", changedDigest),
				cause:  fs.ErrInvalid,
			},
		},
		{
			name: "missing entry",
			source: func() fs.FS {
				value := fixtureFS()
				delete(value, "run.sh")
				return value
			},
			want: validationExpectation{
				stage:  "bundle source ownership check",
				entry:  "run.sh",
				rule:   "every declared entry exists",
				reason: "the manifest entry is missing from the source filesystem",
				impact: "the bundle would be incomplete relative to its ownership inventory",
				fix:    "add the matching source entry or remove it from the manifest",
				cause:  fs.ErrNotExist,
			},
		},
		{
			name: "extra entry",
			source: func() fs.FS {
				value := fixtureFS()
				value["extra.txt"] = &fstest.MapFile{Data: []byte("extra")}
				return value
			},
			want: validationExpectation{
				stage:  "bundle source ownership check",
				entry:  "extra.txt",
				rule:   "no extra source file",
				reason: "the source contains a regular file absent from the manifest",
				impact: "undeclared bytes would bypass content identity and ownership checks",
				fix:    "remove the extra file or add a matching regular-file entry and digest to the manifest",
				cause:  fs.ErrExist,
			},
		},
		{
			name: "type mismatch",
			source: func() fs.FS {
				value := fixtureFS()
				value["run.sh"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
				return value
			},
			want: validationExpectation{
				stage:  "bundle source type validation",
				entry:  "run.sh",
				rule:   "source type matches manifest",
				reason: "manifest declares regular-file but source is directory",
				impact: "the same path would have conflicting ownership semantics",
				fix:    "change the source entry to regular-file or declare its actual type",
				cause:  fs.ErrInvalid,
			},
		},
		{
			name:   "nil source",
			source: func() fs.FS { return nil },
			want: validationExpectation{
				stage:  "bundle source snapshot",
				entry:  "",
				rule:   "non-nil source filesystem",
				reason: "the source fs.FS is nil",
				impact: "no artifact bytes can be validated or captured",
				fix:    "provide an fs.FS containing exactly the manifest's files and directories",
				cause:  fs.ErrInvalid,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := artifact.NewBundle(test.source(), manifest)
			assertExactValidation(t, err, candidateID, test.want)
		})
	}
}

func TestBundleFaultBoundariesPreserveStructuredErrorsAndCauses(t *testing.T) {
	t.Parallel()

	manifest := fixtureManifest(t)
	candidateID := fixtureCandidateID(t, manifest)
	walkErr := errors.New("walk fault")
	metadataErr := errors.New("metadata fault")
	openErr := errors.New("open fault")
	readErr := errors.New("read fault")
	closeErr := errors.New("close fault")
	readFileClosed := false

	tests := []struct {
		name   string
		source fs.FS
		want   validationExpectation
	}{
		{
			name:   "walk",
			source: &faultFS{base: fixtureFS(), readDirErrorPath: ".", readDirErr: walkErr},
			want: validationExpectation{
				stage:  "bundle source walk",
				entry:  ".",
				rule:   "readable source tree",
				reason: "the source filesystem could not enumerate this location: walk fault",
				impact: "the complete artifact inventory cannot be verified",
				fix:    "repair the source filesystem so every declared entry and parent directory can be read",
				cause:  walkErr,
			},
		},
		{
			name:   "metadata",
			source: &faultFS{base: fixtureFS(), metadataErrorPath: "config/settings.json", metadataErr: metadataErr},
			want: validationExpectation{
				stage:  "bundle source inspection",
				entry:  "config/settings.json",
				rule:   "readable source metadata",
				reason: "the source entry metadata could not be read: metadata fault",
				impact: "entry type cannot be checked against the manifest",
				fix:    "repair the source fs.FS so DirEntry.Info succeeds",
				cause:  metadataErr,
			},
		},
		{
			name:   "inconsistent metadata",
			source: &faultFS{base: fixtureFS(), inconsistentPath: "config/settings.json"},
			want: validationExpectation{
				stage:  "bundle source inspection",
				entry:  "config/settings.json",
				rule:   "consistent source metadata",
				reason: fmt.Sprintf("DirEntry directory status disagrees with FileInfo mode %s", (fs.ModeDir | 0o755).Type()),
				impact: "a changing or malicious source could be interpreted differently across reads",
				fix:    "provide a stable fs.FS whose DirEntry and FileInfo report the same type",
				cause:  fs.ErrInvalid,
			},
		},
		{
			name:   "duplicate enumeration",
			source: &faultFS{base: fixtureFS(), duplicatePath: "run.sh"},
			want: validationExpectation{
				stage:  "bundle source walk",
				entry:  "run.sh",
				rule:   "unique source path",
				reason: `the source enumerated path "run.sh" 2 times`,
				impact: "source ownership is ambiguous and cannot be snapshotted deterministically",
				fix:    "repair the fs.FS implementation so each path is enumerated exactly once",
				cause:  fs.ErrExist,
			},
		},
		{
			name:   "open",
			source: &faultFS{base: fixtureFS(), fileFaultPath: "run.sh", openErr: openErr},
			want: validationExpectation{
				stage:  "bundle source read",
				entry:  "run.sh",
				rule:   "open declared regular file",
				reason: "the source file could not be opened: open fault",
				impact: "its exact bytes cannot be validated or snapshotted",
				fix:    "repair the fs.FS so the declared file opens for reading",
				cause:  openErr,
			},
		},
		{
			name: "read",
			source: &faultFS{
				base:          fixtureFS(),
				fileFaultPath: "run.sh",
				readErr:       readErr,
				closeObserved: &readFileClosed,
			},
			want: validationExpectation{
				stage:  "bundle source read",
				entry:  "run.sh",
				rule:   "read complete declared file",
				reason: "the source file could not be read completely: read fault",
				impact: "its exact bytes cannot be validated or snapshotted",
				fix:    "repair the fs.FS so reading reaches a clean EOF",
				cause:  readErr,
			},
		},
		{
			name:   "close",
			source: &faultFS{base: fixtureFS(), fileFaultPath: "run.sh", closeErr: closeErr},
			want: validationExpectation{
				stage:  "bundle source close",
				entry:  "run.sh",
				rule:   "close source file after snapshot",
				reason: "the source file reported a close failure: close fault",
				impact: "source resource ownership cannot be completed reliably",
				fix:    "repair the fs.File implementation so Close succeeds",
				cause:  closeErr,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, firstErr := artifact.NewBundle(test.source, manifest)
			_, repeatedErr := artifact.NewBundle(test.source, manifest)
			assertExactValidation(t, firstErr, candidateID, test.want)
			assertExactValidation(t, repeatedErr, candidateID, test.want)
			if firstErr.Error() != repeatedErr.Error() {
				t.Fatalf("repeated injected failure was not deterministic:\nfirst:  %v\nsecond: %v", firstErr, repeatedErr)
			}
		})
	}
	if !readFileClosed {
		t.Fatal("source file Close was not called after Read failed")
	}
}

func TestBundleInvalidSelectionIgnoresEnumerationOrder(t *testing.T) {
	t.Parallel()

	manifest := fixtureManifest(t)
	candidateID := fixtureCandidateID(t, manifest)
	invalidSource := fixtureFS()
	invalidSource["a-extra.txt"] = &fstest.MapFile{Data: []byte("a")}
	invalidSource["z-extra.txt"] = &fstest.MapFile{Data: []byte("z")}

	_, orderedErr := artifact.NewBundle(&faultFS{base: invalidSource}, manifest)
	_, reversedErr := artifact.NewBundle(&faultFS{base: invalidSource, reverse: true}, manifest)
	want := validationExpectation{
		stage:  "bundle source ownership check",
		entry:  "a-extra.txt",
		rule:   "no extra source file",
		reason: "the source contains a regular file absent from the manifest",
		impact: "undeclared bytes would bypass content identity and ownership checks",
		fix:    "remove the extra file or add a matching regular-file entry and digest to the manifest",
		cause:  fs.ErrExist,
	}
	assertExactValidation(t, orderedErr, candidateID, want)
	assertExactValidation(t, reversedErr, candidateID, want)
	if orderedErr.Error() != reversedErr.Error() {
		t.Fatalf("enumeration order selected different errors:\nordered:  %v\nreversed: %v", orderedErr, reversedErr)
	}
}

type validationExpectation struct {
	stage  string
	entry  string
	rule   string
	reason string
	impact string
	fix    string
	cause  error
}

func assertExactValidation(t *testing.T, err error, bundle string, want validationExpectation) {
	t.Helper()
	if err == nil {
		t.Fatal("NewBundle succeeded, want validation error")
	}
	var validation *artifact.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T %v, want *artifact.ValidationError", err, err)
	}
	if validation.Bundle != bundle || validation.Stage != want.stage || validation.Entry != want.entry ||
		validation.Rule != want.rule || validation.Reason != want.reason || validation.Impact != want.impact ||
		validation.Fix != want.fix {
		t.Fatalf("validation mismatch:\n got: %#v\nwant: bundle=%q stage=%q entry=%q rule=%q reason=%q impact=%q fix=%q",
			validation, bundle, want.stage, want.entry, want.rule, want.reason, want.impact, want.fix)
	}
	if want.cause != nil && !errors.Is(err, want.cause) {
		t.Fatalf("validation cause = %v, want errors.Is(%v)", validation.Cause, want.cause)
	}
	if validation.Cause != want.cause {
		t.Fatalf("validation direct cause = %v, want exact cause %v", validation.Cause, want.cause)
	}
}

func fixtureCandidateID(t *testing.T, manifest artifact.Manifest) string {
	t.Helper()
	bundle, err := artifact.NewBundle(fixtureFS(), manifest)
	if err != nil {
		t.Fatalf("NewBundle(candidate fixture): %v", err)
	}
	return bundle.ID().String()
}

type faultFS struct {
	base fstest.MapFS

	readDirErrorPath  string
	readDirErr        error
	metadataErrorPath string
	metadataErr       error
	inconsistentPath  string
	duplicatePath     string
	reverse           bool

	fileFaultPath string
	openErr       error
	readErr       error
	closeErr      error
	closeObserved *bool
}

func (f *faultFS) Open(name string) (fs.File, error) {
	if name == f.fileFaultPath && f.openErr != nil {
		return nil, f.openErr
	}
	file, err := f.base.Open(name)
	if err != nil {
		return nil, err
	}
	if name == f.fileFaultPath && (f.readErr != nil || f.closeErr != nil || f.closeObserved != nil) {
		return &faultFile{File: file, readErr: f.readErr, closeErr: f.closeErr, closeObserved: f.closeObserved}, nil
	}
	return file, nil
}

func (f *faultFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.readDirErrorPath {
		return nil, f.readDirErr
	}
	entries, err := fs.ReadDir(f.base, name)
	if err != nil {
		return nil, err
	}
	for index, entry := range entries {
		fullPath := entry.Name()
		if name != "." {
			fullPath = name + "/" + entry.Name()
		}
		switch fullPath {
		case f.metadataErrorPath:
			entries[index] = faultDirEntry{DirEntry: entry, infoErr: f.metadataErr}
		case f.inconsistentPath:
			info, infoErr := entry.Info()
			if infoErr != nil {
				return nil, infoErr
			}
			entries[index] = faultDirEntry{DirEntry: entry, info: modeFileInfo{FileInfo: info, mode: fs.ModeDir | 0o755}}
		}
		if fullPath == f.duplicatePath {
			entries = append(entries, entries[index])
		}
	}
	if f.reverse {
		for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
			entries[left], entries[right] = entries[right], entries[left]
		}
	}
	return entries, nil
}

type faultDirEntry struct {
	fs.DirEntry
	info    fs.FileInfo
	infoErr error
}

func (e faultDirEntry) Info() (fs.FileInfo, error) {
	if e.infoErr != nil {
		return nil, e.infoErr
	}
	return e.info, nil
}

type modeFileInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (i modeFileInfo) Mode() fs.FileMode { return i.mode }
func (i modeFileInfo) IsDir() bool       { return i.mode.IsDir() }

type faultFile struct {
	fs.File
	readErr       error
	closeErr      error
	closeObserved *bool
}

func (f *faultFile) Read(destination []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.File.Read(destination)
}

func (f *faultFile) Close() error {
	if f.closeObserved != nil {
		*f.closeObserved = true
	}
	underlyingErr := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return underlyingErr
}

var _ fs.FileInfo = modeFileInfo{}
