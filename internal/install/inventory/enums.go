package inventory

import "github.com/dayvidpham/pasture/internal/install/cell"

// Source records which control plane last mutated a cell.
type Source struct{ name string }

const (
	sourceInstaller   = "installer"
	sourceHomeManager = "home-manager"
)

// InstallerSource marks a cell mutated by the interactive installer.
func InstallerSource() Source { return Source{name: sourceInstaller} }

// HomeManagerSource marks a cell mutated by Home Manager activation.
func HomeManagerSource() Source { return Source{name: sourceHomeManager} }

// ParseSource resolves a canonical source name.
func ParseSource(value string) (Source, error) {
	switch value {
	case sourceInstaller:
		return InstallerSource(), nil
	case sourceHomeManager:
		return HomeManagerSource(), nil
	default:
		return Source{}, cell.NewFault(
			"source parse", "known control source",
			"the source is not installer or home-manager",
			"internal/install/inventory.ParseSource", "decoding an inventory record",
			"an unknown control source could hide mixed-controller conflicts",
			"use exactly installer or home-manager", nil,
		)
	}
}

func (s Source) String() string { return s.name }
func (s Source) IsValid() bool {
	return s.name == sourceInstaller || s.name == sourceHomeManager
}

// Observation is the strongest live-confirmed state of a cell.
type Observation struct{ name string }

const (
	observationInstalled = "installed"
	observationAbsent    = "absent"
	observationUnknown   = "unknown"
)

// Installed reports the cell is confirmed present.
func Installed() Observation { return Observation{name: observationInstalled} }

// Absent reports the cell is confirmed removed (a tombstone).
func Absent() Observation { return Observation{name: observationAbsent} }

// Unknown reports the cell state could not be confirmed after an action.
func Unknown() Observation { return Observation{name: observationUnknown} }

// ParseObservation resolves a canonical observation name.
func ParseObservation(value string) (Observation, error) {
	switch value {
	case observationInstalled:
		return Installed(), nil
	case observationAbsent:
		return Absent(), nil
	case observationUnknown:
		return Unknown(), nil
	default:
		return Observation{}, cell.NewFault(
			"observation parse", "known observation",
			"the observation is not installed, absent, or unknown",
			"internal/install/inventory.ParseObservation", "decoding an inventory record",
			"an unknown observation could misreport what remains installed",
			"use installed, absent, or unknown", nil,
		)
	}
}

func (o Observation) String() string { return o.name }
func (o Observation) IsValid() bool {
	switch o.name {
	case observationInstalled, observationAbsent, observationUnknown:
		return true
	default:
		return false
	}
}

// Trust is the native-trust disposition of a cell.
type Trust struct{ name string }

const (
	trustNotApplicable = "not-applicable"
	trustPending       = "pending"
	trustTrusted       = "trusted"
)

// TrustNotApplicable is used for cells whose harness has no trust gate.
func TrustNotApplicable() Trust { return Trust{name: trustNotApplicable} }

// TrustPending marks a native cell awaiting the host's out-of-band review.
func TrustPending() Trust { return Trust{name: trustPending} }

// TrustTrusted marks a cell the host has approved.
func TrustTrusted() Trust { return Trust{name: trustTrusted} }

// ParseTrust resolves a canonical trust name.
func ParseTrust(value string) (Trust, error) {
	switch value {
	case trustNotApplicable:
		return TrustNotApplicable(), nil
	case trustPending:
		return TrustPending(), nil
	case trustTrusted:
		return TrustTrusted(), nil
	default:
		return Trust{}, cell.NewFault(
			"trust parse", "known trust disposition",
			"the trust value is not not-applicable, pending, or trusted",
			"internal/install/inventory.ParseTrust", "decoding an inventory record",
			"an unknown trust disposition could claim hooks are active before approval",
			"use not-applicable, pending, or trusted", nil,
		)
	}
}

func (t Trust) String() string { return t.name }
func (t Trust) IsValid() bool {
	switch t.name {
	case trustNotApplicable, trustPending, trustTrusted:
		return true
	default:
		return false
	}
}
