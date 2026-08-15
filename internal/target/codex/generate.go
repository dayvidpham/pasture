package codex

// The embedded snapshot is generated from the canonical Codex emitter. Runtime
// descriptor construction reads only that snapshot and never needs a checkout.
//
//go:generate go run ./cmd/generate
