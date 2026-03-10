package errors_test

import (
	stderrors "errors"
	"bytes"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/errors"
)

// ---- Category tests --------------------------------------------------------

func TestCategoryValues(t *testing.T) {
	cases := []struct {
		cat  errors.Category
		want string
	}{
		{errors.CategoryConnection, "connection error"},
		{errors.CategoryWorkflow, "workflow error"},
		{errors.CategoryValidation, "validation error"},
		{errors.CategoryConfig, "config error"},
	}
	for _, tc := range cases {
		if string(tc.cat) != tc.want {
			t.Errorf("Category %q: got %q, want %q", tc.cat, string(tc.cat), tc.want)
		}
	}
}

// ---- StructuredError.Error() -----------------------------------------------

func TestStructuredError_ErrorFormat(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryConnection,
		What:     "cannot reach Temporal",
		Why:      "TCP refused on localhost:7233",
		Impact:   "no workflows can be started",
		Fix:      "start temporal server or set TEMPORAL_ADDRESS",
	}

	got := se.Error()
	if got != "connection error: cannot reach Temporal" {
		t.Errorf("Error() = %q, want %q", got, "connection error: cannot reach Temporal")
	}
}

func TestStructuredError_ImplementsErrorInterface(t *testing.T) {
	var _ error = &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     "missing workflow ID",
	}
}

// ---- StructuredError.Report() ----------------------------------------------

func TestStructuredError_Report_ContainsAllFields(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryConfig,
		What:     "invalid config file",
		Why:      "YAML parse failed at line 5",
		Impact:   "daemon cannot start",
		Fix:      "fix the YAML syntax in ~/.config/pasture/config.yaml",
	}

	var buf bytes.Buffer
	se.Report(&buf)
	out := buf.String()

	checks := []string{
		"config error",
		"invalid config file",
		"YAML parse failed at line 5",
		"daemon cannot start",
		"fix the YAML syntax",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("Report() missing %q in output:\n%s", want, out)
		}
	}
}

func TestStructuredError_Report_Format(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryWorkflow,
		What:     "workflow timed out",
		Why:      "no activity heartbeat for 60s",
		Impact:   "session left in unknown state",
		Fix:      "check worker logs and re-run the signal",
	}

	var buf bytes.Buffer
	se.Report(&buf)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")

	if len(lines) < 4 {
		t.Fatalf("Report() produced %d lines, want at least 4", len(lines))
	}
	if !strings.HasPrefix(lines[0], "workflow error:") {
		t.Errorf("line 0 = %q, want prefix %q", lines[0], "workflow error:")
	}
	if !strings.HasPrefix(lines[1], "  why:") {
		t.Errorf("line 1 = %q, want prefix %q", lines[1], "  why:")
	}
	if !strings.HasPrefix(lines[2], "  impact:") {
		t.Errorf("line 2 = %q, want prefix %q", lines[2], "  impact:")
	}
	if !strings.HasPrefix(lines[3], "  fix:") {
		t.Errorf("line 3 = %q, want prefix %q", lines[3], "  fix:")
	}
}

// ---- errors.As() extraction ------------------------------------------------

func TestStructuredError_ErrorsAs_DirectPointer(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     "missing --session-id flag",
		Why:      "flag not provided",
		Impact:   "command aborted",
		Fix:      "pass --session-id <id>",
	}

	var target *errors.StructuredError
	if !stderrors.As(se, &target) {
		t.Fatal("errors.As() returned false for direct *StructuredError")
	}
	if target.What != "missing --session-id flag" {
		t.Errorf("extracted What = %q, want %q", target.What, "missing --session-id flag")
	}
}

func TestStructuredError_ErrorsAs_WrappedInFmtErrorf(t *testing.T) {
	inner := &errors.StructuredError{
		Category: errors.CategoryConnection,
		What:     "dial failed",
		Why:      "connection refused",
		Impact:   "cannot start session",
		Fix:      "check server is running",
	}
	wrapped := stderrors.Join(stderrors.New("outer context"), inner)

	var target *errors.StructuredError
	if !stderrors.As(wrapped, &target) {
		t.Fatal("errors.As() returned false for wrapped *StructuredError")
	}
	if target.Category != errors.CategoryConnection {
		t.Errorf("extracted Category = %q, want %q", target.Category, errors.CategoryConnection)
	}
}

// ---- ExitCode() ------------------------------------------------------------

func TestExitCode_Validation(t *testing.T) {
	err := &errors.StructuredError{Category: errors.CategoryValidation, What: "bad input"}
	if got := errors.ExitCode(err); got != 1 {
		t.Errorf("ExitCode(validation) = %d, want 1", got)
	}
}

func TestExitCode_Config(t *testing.T) {
	err := &errors.StructuredError{Category: errors.CategoryConfig, What: "bad config"}
	if got := errors.ExitCode(err); got != 4 {
		t.Errorf("ExitCode(config) = %d, want 4", got)
	}
}

func TestExitCode_Connection(t *testing.T) {
	err := &errors.StructuredError{Category: errors.CategoryConnection, What: "dial failed"}
	if got := errors.ExitCode(err); got != 2 {
		t.Errorf("ExitCode(connection) = %d, want 2", got)
	}
}

func TestExitCode_Workflow(t *testing.T) {
	err := &errors.StructuredError{Category: errors.CategoryWorkflow, What: "timed out"}
	if got := errors.ExitCode(err); got != 3 {
		t.Errorf("ExitCode(workflow) = %d, want 3", got)
	}
}

func TestExitCode_UnknownError(t *testing.T) {
	err := stderrors.New("some unexpected error")
	if got := errors.ExitCode(err); got != 1 {
		t.Errorf("ExitCode(unknown) = %d, want 1", got)
	}
}

func TestExitCode_Nil(t *testing.T) {
	if got := errors.ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d, want 0", got)
	}
}

func TestExitCode_WrappedStructuredError(t *testing.T) {
	inner := &errors.StructuredError{Category: errors.CategoryWorkflow, What: "activity failed"}
	wrapped := stderrors.Join(stderrors.New("context"), inner)
	if got := errors.ExitCode(wrapped); got != 3 {
		t.Errorf("ExitCode(wrapped workflow) = %d, want 3", got)
	}
}
