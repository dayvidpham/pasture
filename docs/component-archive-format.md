# Component archive format (`pasture.component-archive/v1`)

This document is the canonical specification of the archives written by

```
pasture install export-components --version <X.Y.Z> --out <DIR>
```

One archive is written per harness/extension cell of the closed three-by-three
installation matrix. The implementation lives in `internal/install/export`
(`WriteArchive` / `ReadArchive`); the exported archives are consumed by the
aggregate release producer in the Aura repository, which freezes them into an
immutable release. Because a published release can never be corrected, the
format is fully specified here rather than left to the writer.

## Source of truth

Every archive is derived from exactly one `artifact.Bundle` — the same bundle
the installer activates for that cell, obtained from the embedded target
descriptors in `internal/target/{claudecode,opencode,codex}`. The bundle's
manifest supplies the member paths, member kinds, and permission modes. Nothing
in this format re-derives per-harness rules; for example the Claude target's
`0755`-for-`*.sh` rule and OpenCode's flat `0644` reach the archive only because
they are recorded in those bundles' manifests.

## Container

- gzip (RFC 1952) wrapping a tar (POSIX ustar with PAX extensions) stream.
- gzip compression level: `BestCompression`.
- gzip header: empty `NAME`, empty `COMMENT`, zero `MTIME`, `OS` byte `255`
  (unknown). No `FEXTRA`, no `FHCRC`.
- File extension: `.tgz`.

## Members

- The member set is exactly the bundle manifest's entry set — no more, no less.
  A member that is absent from the manifest, or a manifest entry that is absent
  from the archive, makes the archive invalid.
- Member names are the manifest's bundle-relative canonical paths
  (slash-separated, no leading `/`, no `.` or `..` segments). Directory members
  carry a single trailing `/`.
- Member order is lexicographic by path, which is also the manifest's canonical
  order.
- Member kinds are restricted to regular files (`tar.TypeReg`) and directories
  (`tar.TypeDir`). Symlinks, hard links, devices, and every other tar type are
  rejected on both write and read.
- `Mode` is the manifest entry's permission bits (`0000`–`0777`). No type bits,
  setuid, setgid, or sticky bits are ever set.
- `Uid` and `Gid` are `0`; `Uname` and `Gname` are empty.
- `ModTime` is the fixed epoch `1970-01-01T00:00:00Z` for every member.
  `AccessTime` and `ChangeTime` are unset. (The tar zero time is deliberately
  *not* used: it is not representable in a portable header and would force
  format-specific extended records.)
- `Size` is the exact byte length of the regular file; directories carry `0`.
- Regular-file bytes are the bundle's bytes, checked against the manifest's
  declared SHA-256 digest before they are written.

## Determinism

For one Pasture build and one input bundle, the archive bytes are identical
across runs, machines, and output paths: the member list, order, modes,
ownership, and timestamps are all fixed by the manifest or by this
specification, and the gzip header carries no run-specific bytes. Release
pipelines may therefore rebuild an archive from the same Pasture revision and
compare it byte for byte.

The gzip *compressed representation* additionally depends on the Go standard
library's DEFLATE implementation. Reproducing bytes across Pasture builds
requires the same Go toolchain; reproducing the archive's *contents* does not.

## Verification

`ReadArchive` reads an archive back into its member list, recomputing each
regular file's digest from the archived bytes; `VerifyArchive` compares that
list against a bundle manifest, member for member. The export verb runs this
check against every archive it writes before reporting success, so a written
archive is proven to match the target descriptor it claims to carry.

The tests in `internal/install/export` additionally decode real exported
archives with the Go standard library's `archive/tar` and `compress/gzip`,
rather than with this package's own reader, so the format is checked by an
independent decoder.

The **authoritative cross-check against the consumer is the release pipeline**:
it runs the real aggregate release producer over real export output before any
release is published. The producer's document shape is mirrored inside this
package's tests only as a representative fixture — it is a convenience check,
not the contract, and it can drift from the producer without failing here.

## Output directory

`--out DIR` is claimed, never merged: the directory must not already exist, it
is created by the export, and any failure removes it again. A partially written
set therefore cannot be published. The directory contains the nine archives and
one `components.json`.

## `components.json`

`components.json` is the component-set document the aggregate release producer
consumes. It uses the schema `aura.aggregate-components/v1` and contains
exactly nine records in the canonical component order, each with:

| Field | Value |
|---|---|
| `id` | the canonical `<harness>/<extension>` component identity |
| `artifact` | the archive path, relative to `components.json` (always its basename, so `artifact` and `asset` are identical in exported documents) |
| `asset` | the canonical release asset basename |
| `bundle_id` | the exact `artifact.BundleID` of the cell's target bundle |

The asset basename is `pasture-<version>-<claude|opencode|codex>-<skills|agents|hooks>.tgz`
(Claude Code is spelled `claude` in asset names while its component identity
remains `claude-code`). The archive digests are not recorded here: the producer
computes them from the exact bytes it freezes, and publishes them in its own
aggregate manifest. The export verb also prints them, and `--json` reports them
as a machine-readable document.
