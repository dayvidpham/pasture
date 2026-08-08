package main

// hookLifecycleRawUse is the visible extra path segment that marks the M4 raw
// ingestion escape hatch (URD R4.1, R4.3c). The path segment itself IS the
// visible mark: the native surface never suggests raw, and raw is never the
// default path (authority §10).
const hookLifecycleRawUse = "raw"

// hookLifecycleRawBanner is the mandatory non-recommended marking for the raw
// subcommand's help text (URD R4.3c, UAT-Q4 "raw ingestion — for imports and
// migration; not the default path."). It states the escape hatch's purpose,
// that it is for imports and migration, and that it is NOT the default path.
//
// SLICE-2 owns the `hook lifecycle raw` command itself
// (cmd/pasture/hook_lifecycle_raw.go); its cobra command must render this
// banner verbatim as its Long help text so `pasture hook lifecycle raw --help`
// leads with the non-recommended posture.
const hookLifecycleRawBanner = "raw ingestion — for imports and migration; not the default path."

// hookLifecycleRawLong is the full Long text the raw subcommand must render.
// It opens with the exact banner, then documents the dry-run preview. Any
// additional usage prose must stay after the banner so the non-recommended
// marking remains the visible opener.
const hookLifecycleRawLong = hookLifecycleRawBanner + `

Use --dry-run to preview what a raw ingestion would commit: the payload is
classified, admitted, and verified exactly as a real ingestion would be (same
activation posture, same L1→L2 derivation tail), but no database is opened and
no receipt is written. The preview names the origin, wire schema identity,
harness/event co-ordinates, effects, and the canonical host continuation.
Because dry-run performs no I/O, a later real ingestion can still fail while
opening or writing the store.`
