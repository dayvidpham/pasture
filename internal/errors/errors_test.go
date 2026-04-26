package errors_test

import (
	"bytes"
	stderrors "errors"
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
		{errors.CategoryStorage, "storage error"},
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
		What:     "The configuration file couldn't be loaded.",
		Why:      "The YAML on line 5 is malformed.",
		Impact:   "The daemon can't start without a valid configuration.",
		Fix: "1. Open the file and fix the YAML syntax on line 5:\n" +
			"     $EDITOR ~/.config/pasture/config.yaml",
	}

	var buf bytes.Buffer
	se.Report(&buf)
	out := buf.String()

	checks := []string{
		// Top "Error:" line surfaces the What as a plain sentence.
		"Error: The configuration file couldn't be loaded.",
		// Full English labels, NOT lowercase shorthand.
		"Problem:",
		"Reason:",
		"Impact:",
		"How to fix:",
		// Substantive content from each field.
		"The YAML on line 5 is malformed.",
		"The daemon can't start without a valid configuration.",
		"$EDITOR ~/.config/pasture/config.yaml",
		// Category MUST NOT appear in the user-visible output.
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("Report() missing %q in output:\n%s", want, out)
		}
	}

	// The literal category string ("config error") must NOT leak into the
	// user-visible block — the prose conveys the category implicitly.
	if strings.Contains(out, "config error") {
		t.Errorf("Report() leaked the category literal into user-visible output:\n%s", out)
	}
}

func TestStructuredError_Report_Format(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryWorkflow,
		What:     "The session ran past its timeout.",
		Why:      "No worker reported activity for 60 seconds.",
		Impact:   "The session is stuck and cannot continue.",
		Fix: "1. Check the worker logs for stalls:\n" +
			"     pastured logs --tail=200\n" +
			"2. Re-send the signal once the worker is healthy:\n" +
			"     pasture-msg <signal-args>",
	}

	var buf bytes.Buffer
	se.Report(&buf)
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	if len(lines) < 6 {
		t.Fatalf("Report() produced %d lines, want at least 6:\n%s", len(lines), out)
	}

	// Line 0: top "Error:" line with the plain-English What.
	if !strings.HasPrefix(lines[0], "Error: ") {
		t.Errorf("line 0 = %q, want prefix %q", lines[0], "Error: ")
	}
	// Line 1: blank separator between header and labelled block.
	if lines[1] != "" {
		t.Errorf("line 1 = %q, want empty separator line", lines[1])
	}

	// Locate each labelled line. Order is fixed: Problem, Reason, Impact,
	// then "How to fix:" with the Fix body indented underneath.
	wantLabels := []string{
		"  Problem:",
		"  Reason:",
		"  Impact:",
		"  How to fix:",
	}
	for _, want := range wantLabels {
		found := false
		for _, line := range lines {
			if strings.HasPrefix(line, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Report() missing label %q. Full output:\n%s", want, out)
		}
	}

	// The "How to fix:" body MUST be indented 4 spaces so steps and
	// commands hang under the label rather than aligning with it.
	if !strings.Contains(out, "    1. Check the worker logs for stalls:") {
		t.Errorf("Report() did not indent fix step. Full output:\n%s", out)
	}
	if !strings.Contains(out, "         pastured logs --tail=200") {
		t.Errorf("Report() did not preserve indented command. Full output:\n%s", out)
	}
}

// TestStructuredError_Report_LabelsAreVerticallyAligned guarantees that
// multi-line values hang under the value column rather than wrapping back
// to column zero. This is the visual property that makes the block easy
// to scan.
func TestStructuredError_Report_LabelsAreVerticallyAligned(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     "The ID you provided is not valid.",
		Why:      "It is missing the required separator.\nWe expect IDs of the form \"project--uuid\".",
		Impact:   "The epoch can't be started.",
		Fix:      "1. Generate a fresh ID:\n     pasture task create REQUEST --type=feature \"<title>\"",
	}

	var buf bytes.Buffer
	se.Report(&buf)
	out := buf.String()

	// The continuation line of the Reason value should hang under the
	// value column (15 leading spaces: "  " indent + 12-wide label column
	// + 1 separator space).
	wantContinuation := strings.Repeat(" ", 15) + "We expect IDs of the form"
	if !strings.Contains(out, wantContinuation) {
		t.Errorf("Report() did not align Reason continuation under value column. Output:\n%s", out)
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

// TestExitCode_Storage verifies that CategoryStorage maps to exit code 5
// (PROPOSAL-2 §7.10.5 / IMPL_PLAN §1.4). Used by:
//   - audit migration failures (Migrate)
//   - newer-schema-than-binary rejection (Scenario 5)
//   - SQLite open / write failures in the unified pasture.db
func TestExitCode_Storage(t *testing.T) {
	err := &errors.StructuredError{Category: errors.CategoryStorage, What: "schema migration failed"}
	if got := errors.ExitCode(err); got != 5 {
		t.Errorf("ExitCode(storage) = %d, want 5", got)
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
