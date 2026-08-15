package opencode

// The embedded assets are a source-free snapshot of the OpenCode harness output.
// The generator partitions the live harness output into independently installable
// skills, agents, and hooks bundles without changing their bytes.
//
//go:generate go run asset_generate.go
