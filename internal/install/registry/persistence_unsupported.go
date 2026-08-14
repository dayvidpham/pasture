//go:build !linux && !darwin

package registry

// Registry persistence is intentionally restricted to platforms where this
// package implements descriptor-relative, no-follow traversal and atomic rename.
// Add an equally strong platform implementation before enabling another target.
var _ = registryPersistenceRequiresDescriptorRelativeNoFollowSupport
