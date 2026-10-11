package audit

import (
	"fmt"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

func contextValidationError(operation string) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation,
		What: "The audit operation has no context.", Why: "A nil context cannot govern database acquisition or cancellation.",
		Where:  fmt.Sprintf("audit.%s during entry validation (internal/audit)", operation),
		Impact: "No database work was started.", Fix: "Supply a live context, or use the Background compatibility entry point."}
}

func migrationCancelledError(stage string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryStorage,
		What: "The audit migration was cancelled.", Why: cause.Error(),
		Where:  "audit migration, " + stage + " (internal/audit/migrate.go)",
		Impact: "No tracker was returned. An uncommitted step is rolled back; earlier whole steps remain committed. Inspect the recorded version if cancellation coincided with commit.",
		Fix:    "Retry migration with a live context after the lock holder finishes; the recorded version selects the remaining steps.", Cause: cause}
}
