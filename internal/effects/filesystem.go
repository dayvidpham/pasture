package effects

// FileSystemEffectKind is the closed set of modeled filesystem effects.
type FileSystemEffectKind string

const (
	// FSRead reads the exact content of one owned path.
	FSRead FileSystemEffectKind = "read"
	// FSWriteReplace writes exact content to one owned path, replacing any
	// prior content.
	FSWriteReplace FileSystemEffectKind = "write-replace"
	// FSCreateDirectory creates one owned directory path.
	FSCreateDirectory FileSystemEffectKind = "create-directory"
	// FSMove moves one owned path to another owned path.
	FSMove FileSystemEffectKind = "move"
	// FSRemove removes exactly one owned path.
	FSRemove FileSystemEffectKind = "remove"
)

// FileSystemEffect is the closed sum of modeled filesystem effects. Every
// variant names exact owned paths; none can name a glob, so a removal can never
// expand to an unowned set of files. It is an Effect variant, classified native.
type FileSystemEffect struct {
	kind        FileSystemEffectKind
	path        OwnedPath
	destination OwnedPath
	content     []byte
	constructed bool
}

// NewReadFile reads the exact content of one owned path.
func NewReadFile(target OwnedPath) (FileSystemEffect, error) {
	return newSinglePathFSEffect(FSRead, target)
}

// NewWriteReplaceFile writes exact content to one owned path.
func NewWriteReplaceFile(target OwnedPath, content []byte) (FileSystemEffect, error) {
	if !target.IsValid() {
		return FileSystemEffect{}, invalidFSPath(FSWriteReplace)
	}
	return FileSystemEffect{
		kind:        FSWriteReplace,
		path:        target,
		content:     append([]byte(nil), content...),
		constructed: true,
	}, nil
}

// NewCreateDirectory creates one owned directory path.
func NewCreateDirectory(target OwnedPath) (FileSystemEffect, error) {
	return newSinglePathFSEffect(FSCreateDirectory, target)
}

// NewMoveFile moves one owned path to another owned path.
func NewMoveFile(from, to OwnedPath) (FileSystemEffect, error) {
	if !from.IsValid() || !to.IsValid() {
		return FileSystemEffect{}, invalidFSPath(FSMove)
	}
	if from.Equal(to) {
		return FileSystemEffect{}, effectError(
			"move source and destination are identical",
			"a move must relocate a path to a different owned path",
			"NewMoveFile", "filesystem effect validation",
			"the effect would be a no-op or an ambiguous self-move",
			"supply distinct source and destination owned paths", nil,
		)
	}
	return FileSystemEffect{kind: FSMove, path: from, destination: to, constructed: true}, nil
}

// NewRemoveFile removes exactly one owned path. It cannot name a glob, so it can
// never expand to an unowned set of files.
func NewRemoveFile(target OwnedPath) (FileSystemEffect, error) {
	return newSinglePathFSEffect(FSRemove, target)
}

func newSinglePathFSEffect(kind FileSystemEffectKind, target OwnedPath) (FileSystemEffect, error) {
	if !target.IsValid() {
		return FileSystemEffect{}, invalidFSPath(kind)
	}
	return FileSystemEffect{kind: kind, path: target, constructed: true}, nil
}

func invalidFSPath(kind FileSystemEffectKind) error {
	return effectError(
		"filesystem effect path is zero or invalid",
		"a filesystem effect must name an exact constructor-validated owned path",
		"New"+string(kind)+" filesystem effect", "filesystem effect validation",
		"the effect has no exact target and could act on the wrong path",
		"construct every path with NewOwnedPath", nil,
	)
}

func (f FileSystemEffect) Kind() FileSystemEffectKind { return f.kind }
func (f FileSystemEffect) Path() OwnedPath            { return f.path }
func (f FileSystemEffect) IsValid() bool              { return f.constructed && f.path.IsValid() }

// Destination returns the move destination and true for a move effect.
func (f FileSystemEffect) Destination() (OwnedPath, bool) {
	if f.kind != FSMove {
		return OwnedPath{}, false
	}
	return f.destination, true
}

// Content returns a defensive copy of the write content and true for a
// write-replace effect.
func (f FileSystemEffect) Content() ([]byte, bool) {
	if f.kind != FSWriteReplace {
		return nil, false
	}
	return append([]byte(nil), f.content...), true
}

// StateChanging reports whether the effect mutates the filesystem. Only FSRead
// is read-only.
func (f FileSystemEffect) StateChanging() bool { return f.kind != FSRead }

// Classify reports the runtime class. Filesystem effects are executed directly
// by the host runtime.
func (f FileSystemEffect) Classify() RuntimeClass { return RuntimeClassNative }

func (f FileSystemEffect) isEffect() {}
