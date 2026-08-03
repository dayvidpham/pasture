package activation

import (
	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// ActivationStrategy is the sealed set of ways one component is activated. The
// sealing method is unexported so only this package can define variants:
// NativePlugin, DirectFile, and NativePluginPendingTrust. A component holds
// exactly one strategy, which prevents invalid nullable manager/config
// combinations.
type ActivationStrategy interface {
	activationStrategy()
	// Kind returns a stable discriminator for goldens and diagnostics.
	Kind() StrategyKind
}

// StrategyKind is a typed discriminator over the closed strategy set.
type StrategyKind struct{ name string }

var (
	nativePluginKind      = StrategyKind{name: "native-plugin"}
	directFileKind        = StrategyKind{name: "direct-file"}
	nativePendingKind     = StrategyKind{name: "native-plugin-pending-trust"}
	canonicalStrategyKind = [...]StrategyKind{nativePluginKind, directFileKind, nativePendingKind}
)

// NativePluginKindValue, DirectFileKindValue, and NativePluginPendingTrustKindValue
// expose the discriminators for switch comparisons.
func NativePluginKindValue() StrategyKind             { return nativePluginKind }
func DirectFileKindValue() StrategyKind               { return directFileKind }
func NativePluginPendingTrustKindValue() StrategyKind { return nativePendingKind }

// String returns the stable discriminator name.
func (k StrategyKind) String() string { return k.name }

// IsValid reports whether k is one of the closed strategy discriminators.
func (k StrategyKind) IsValid() bool {
	for _, candidate := range canonicalStrategyKind {
		if candidate.name == k.name {
			return true
		}
	}
	return false
}

// ParseStrategyKind resolves a canonical strategy discriminator name.
func ParseStrategyKind(value string) (StrategyKind, error) {
	for _, candidate := range canonicalStrategyKind {
		if candidate.name == value {
			return candidate, nil
		}
	}
	return StrategyKind{}, cell.NewFault(
		"strategy kind parse", "known strategy discriminator",
		"the strategy kind is not native-plugin, direct-file, or native-plugin-pending-trust",
		"internal/install/activation.ParseStrategyKind", "decoding a strategy discriminator",
		"an unknown strategy could not be reconciled to a native or direct-file action",
		"use native-plugin, direct-file, or native-plugin-pending-trust", nil,
	)
}

// NativePlugin activates a component through a harness's documented native
// plugin/marketplace manager. It carries an exact package identity and the
// literal manager argv schemas; it owns no filesystem destination.
type NativePlugin struct {
	pkg      string
	managers []CommandSchema
	valid    bool
}

// NewNativePlugin validates a native-plugin strategy: a non-empty package
// identity and at least one manager command schema.
func NewNativePlugin(pkg string, managers ...CommandSchema) (NativePlugin, error) {
	if pkg == "" {
		return NativePlugin{}, cell.NewFault(
			"native strategy construction", "non-empty package identity",
			"the native plugin package identity is empty",
			"internal/install/activation.NewNativePlugin", "binding a native plugin strategy",
			"the native manager cannot select a package to install",
			"provide the exact plugin package identity, e.g. pasture-skills", nil,
		)
	}
	if len(managers) == 0 {
		return NativePlugin{}, cell.NewFault(
			"native strategy construction", "at least one manager schema",
			"no manager command schema was provided",
			"internal/install/activation.NewNativePlugin", "binding a native plugin strategy",
			"the native manager has no argv to execute",
			"provide the frozen native manager command schemas", nil,
		)
	}
	for _, m := range managers {
		if !m.IsValid() {
			return NativePlugin{}, cell.NewFault(
				"native strategy construction", "valid manager schemas",
				"a manager command schema is the invalid zero value",
				"internal/install/activation.NewNativePlugin", "binding a native plugin strategy",
				"an unvalidated argv could be executed",
				"construct each schema with NewCommandSchema", nil,
			)
		}
	}
	return NativePlugin{pkg: pkg, managers: append([]CommandSchema(nil), managers...), valid: true}, nil
}

func (NativePlugin) activationStrategy() {}
func (NativePlugin) Kind() StrategyKind  { return nativePluginKind }
func (s NativePlugin) Package() string   { return s.pkg }
func (s NativePlugin) IsValid() bool     { return s.valid }
func (s NativePlugin) Managers() []CommandSchema {
	return append([]CommandSchema(nil), s.managers...)
}

// NativePluginPendingTrust is a native-plugin activation whose completion is
// installed-pending-trust: the native host still requires an out-of-band review
// (e.g. Codex hooks). It never claims the component is active before approval
// and writes no private trust state.
type NativePluginPendingTrust struct {
	plugin NativePlugin
}

// NewNativePluginPendingTrust wraps a validated native-plugin strategy as
// pending-trust.
func NewNativePluginPendingTrust(plugin NativePlugin) (NativePluginPendingTrust, error) {
	if !plugin.IsValid() {
		return NativePluginPendingTrust{}, cell.NewFault(
			"pending-trust strategy construction", "valid underlying native plugin",
			"the underlying native plugin strategy is invalid",
			"internal/install/activation.NewNativePluginPendingTrust", "binding a pending-trust strategy",
			"the pending-trust activation has no valid native manager",
			"construct the native plugin with NewNativePlugin first", nil,
		)
	}
	return NativePluginPendingTrust{plugin: plugin}, nil
}

func (NativePluginPendingTrust) activationStrategy() {}
func (NativePluginPendingTrust) Kind() StrategyKind  { return nativePendingKind }
func (s NativePluginPendingTrust) Plugin() NativePlugin {
	return s.plugin
}

// DirectFile activates a component by materializing a published artifact bundle
// under a destination root, adding only the destination to the bundle the
// target already published. It owns no native manager argv.
type DirectFile struct {
	bundle          artifact.Bundle
	destinationRoot string
	valid           bool
}

// NewDirectFile validates a direct-file strategy: a valid bundle and a
// non-empty destination root (a caller-supplied absolute or relative directory,
// resolved by the apply engine).
func NewDirectFile(bundle artifact.Bundle, destinationRoot string) (DirectFile, error) {
	if bundle.ID().String() == "" {
		return DirectFile{}, cell.NewFault(
			"direct-file strategy construction", "published artifact bundle",
			"the artifact bundle is the invalid zero value",
			"internal/install/activation.NewDirectFile", "binding a direct-file strategy",
			"there are no bytes to materialize at the destination",
			"pass the target-published artifact.Bundle", nil,
		)
	}
	if destinationRoot == "" {
		return DirectFile{}, cell.NewFault(
			"direct-file strategy construction", "non-empty destination root",
			"the destination root is empty",
			"internal/install/activation.NewDirectFile", "binding a direct-file strategy",
			"the bundle has nowhere to be written",
			"provide the destination directory relative to the caller-supplied harness root", nil,
		)
	}
	return DirectFile{bundle: bundle, destinationRoot: destinationRoot, valid: true}, nil
}

func (DirectFile) activationStrategy()       {}
func (DirectFile) Kind() StrategyKind        { return directFileKind }
func (s DirectFile) Bundle() artifact.Bundle { return s.bundle }
func (s DirectFile) DestinationRoot() string { return s.destinationRoot }
func (s DirectFile) IsValid() bool           { return s.valid }
