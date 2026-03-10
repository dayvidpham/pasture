package temporal

import (
	"context"
	"fmt"
	"log/slog"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
)

// ─── Search Attribute Key Names ───────────────────────────────────────────────

// SAEpochID is the Temporal search attribute name for epoch ID (TEXT — full-text search).
const SAEpochID = "AuraEpochId"

// SAPhase is the Temporal search attribute name for the current phase (KEYWORD).
const SAPhase = "AuraPhase"

// SARole is the Temporal search attribute name for the current role (KEYWORD).
const SARole = "AuraRole"

// SAStatus is the Temporal search attribute name for workflow status (KEYWORD).
const SAStatus = "AuraStatus"

// SADomain is the Temporal search attribute name for phase domain (KEYWORD).
const SADomain = "AuraDomain"

// SALastEventType is the Temporal search attribute name for the last audit event type (KEYWORD).
const SALastEventType = "AuraLastEventType"

// requiredSearchAttributes maps each required search attribute name to its
// Temporal IndexedValueType. Used by EnsureSearchAttributes to auto-register
// on pastured startup. Port of Python _REQUIRED_SEARCH_ATTRIBUTES.
var requiredSearchAttributes = map[string]enumspb.IndexedValueType{
	SAEpochID:       enumspb.INDEXED_VALUE_TYPE_TEXT,
	SAPhase:         enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	SARole:          enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	SAStatus:        enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	SADomain:        enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	SALastEventType: enumspb.INDEXED_VALUE_TYPE_KEYWORD,
}

// OperatorServiceProvider is the narrow interface required by EnsureSearchAttributes.
// client.Client from go.temporal.io/sdk/client satisfies this interface via its
// OperatorService() method.
//
// Exposing only this narrow interface makes the function trivially testable
// without depending on the full client.Client type.
type OperatorServiceProvider interface {
	OperatorService() operatorservice.OperatorServiceClient
}

// EnsureSearchAttributes idempotently registers all required Aura search
// attributes with the Temporal namespace. Compares requiredSearchAttributes
// against the namespace's existing custom attributes and adds any that are
// missing. Safe to call on every pastured startup — already-registered
// attributes are skipped.
//
// Port of Python ensure_search_attributes() in scripts/aura_protocol/workflow.py.
//
// Args:
//
//	ctx:       Context for the gRPC calls.
//	c:         Temporal client providing OperatorService(). The standard
//	           client.Client from go.temporal.io/sdk/client satisfies this.
//	namespace: Temporal namespace name to register attributes in (e.g. "default").
//	           Must match the namespace used by the pastured worker.
//	logger:    Logger for the "registered" info message. Pass slog.Default() if nil.
//
// Returns an error if the ListSearchAttributes or AddSearchAttributes call fails.
// A partial registration failure (adding one of N attributes) returns the error
// without retrying; call EnsureSearchAttributes again on next startup to catch up.
func EnsureSearchAttributes(ctx context.Context, c OperatorServiceProvider, namespace string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	// Fetch existing custom attributes from the namespace.
	listResp, err := c.OperatorService().ListSearchAttributes(ctx,
		&operatorservice.ListSearchAttributesRequest{
			Namespace: namespace,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"temporal.EnsureSearchAttributes: ListSearchAttributes failed — "+
				"check Temporal server connectivity and namespace %q: %w",
			namespace, err,
		)
	}

	existing := listResp.GetCustomAttributes() // map[string]IndexedValueType

	// Determine which attributes are missing.
	missing := make(map[string]enumspb.IndexedValueType)
	for name, ivType := range requiredSearchAttributes {
		if _, found := existing[name]; !found {
			missing[name] = ivType
		}
	}

	if len(missing) == 0 {
		return nil
	}

	// Register missing attributes.
	_, err = c.OperatorService().AddSearchAttributes(ctx,
		&operatorservice.AddSearchAttributesRequest{
			Namespace:        namespace,
			SearchAttributes: missing,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"temporal.EnsureSearchAttributes: AddSearchAttributes failed for %v — "+
				"check Temporal server permissions: %w",
			missingNames(missing), err,
		)
	}

	logger.Info("registered Temporal search attributes", "namespace", namespace, "attributes", missingNames(missing))
	return nil
}

// missingNames returns the key slice of a map for log output.
func missingNames(m map[string]enumspb.IndexedValueType) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	return names
}

// Compile-time assertion: client.Client satisfies OperatorServiceProvider.
var _ OperatorServiceProvider = (client.Client)(nil)
