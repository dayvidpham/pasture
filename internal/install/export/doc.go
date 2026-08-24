// Package export builds the immutable per-cell component archives and the
// component-set document that an aggregate release producer consumes.
//
// Everything emitted here is derived from the embedded target descriptors the
// installer itself trusts: the archive members, their permission modes, and the
// declared bundle identity all come from one artifact.Bundle per cell. Nothing
// downstream re-derives them.
//
// The archive format is specified in docs/component-archive-format.md and is
// implemented by WriteArchive/ReadArchive in this package.
package export
