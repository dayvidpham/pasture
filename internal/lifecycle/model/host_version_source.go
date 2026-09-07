package model

import "fmt"

// HostVersionSource describes how ingress obtained HostVersion, not the
// identity or version of a running host process. Zero is unspecified legacy
// provenance; it is not an assertion that the caller supplied the version.
type HostVersionSource string

const (
	HostVersionCallerSupplied  HostVersionSource = "caller-supplied"
	HostVersionExecutableQuery HostVersionSource = "executable-query"
)

// ValidateHostVersionSource is the shared closed-set authority for both the
// handler and receipt writer. It makes no version admission or policy decision.
func ValidateHostVersionSource(source HostVersionSource) error {
	switch source {
	case "", HostVersionCallerSupplied, HostVersionExecutableQuery:
		return nil
	default:
		return fmt.Errorf("host version source is not a declared provenance value; no occurrence can be written with it; use caller-supplied or executable-query only for the route that actually produced the version, or leave legacy provenance unspecified")
	}
}
