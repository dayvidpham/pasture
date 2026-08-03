// Package other declares one canonical typed capability identity, consumed
// by a different package through a qualified reference — the cross-package
// reuse case capabilitylint.Check conservatively allows.
package other

import "github.com/dayvidpham/pasture/internal/codegen/ir"

// RenderCapabilityID is a canonical package-level typed const declared in a
// package other than the one that calls DefineCapability with it.
const RenderCapabilityID ir.CapabilityID = "acme.other.render/v1"
