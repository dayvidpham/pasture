package formatters

import (
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/types"
)

// InitResult describes a successfully initialized unified database.
type InitResult struct {
	DBPath        string
	BuiltInAgents int
}

type initResultJSON struct {
	DBPath        string `json:"dbPath"`
	BuiltInAgents int    `json:"builtInAgents"`
}

// FormatInitResult renders the stable output of `pasture init`.
func FormatInitResult(result InitResult, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		wire, err := json.MarshalIndent(initResultJSON{
			DBPath:        result.DBPath,
			BuiltInAgents: result.BuiltInAgents,
		}, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "The database initialization result couldn't be rendered as JSON.",
				Why:      err.Error(),
				Where:    "Formatting database initialization output (internal/formatters/init.go in formatters.FormatInitResult).",
				Impact:   "The database is initialized, but the command can't print its result.",
				Fix:      "Retry with --format text. If that also fails, report this as a Pasture formatting bug.",
				Cause:    err,
			}
		}
		return string(wire), nil
	case types.OutputText:
		return fmt.Sprintf("initialized %s with %d built-in agents", result.DBPath, result.BuiltInAgents), nil
	default:
		return "", unknownFormatErr("FormatInitResult", format)
	}
}
