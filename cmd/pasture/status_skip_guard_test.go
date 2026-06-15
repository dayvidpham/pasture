package main

import (
	"os"
	"strings"
	"testing"
)

func TestStatusStaleSchemaTestsDoNotUseSkipMigrations(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("status_test.go")
	if err != nil {
		t.Fatalf("read status_test.go: %v", err)
	}
	body := string(data)
	for _, name := range []string{
		"TestCLI_Status_StaleSchema_OlderDB_ErrorsAndNeverMigrates",
		"TestCLI_Status_StaleSchema_NewerDB_ErrorsAndNeverMigrates",
		"TestCLI_Status_StaleSchema_OlderDB_ListPath_ErrorsNotSilent",
		"TestCLI_Status_StaleSchema_OlderDB_UnknownEpoch_ErrorsMismatchNotMisspelled",
	} {
		fn := functionBody(t, body, name)
		for _, forbidden := range []string{"WithSkipMigrations", "OpenGoldenTaskTracker", "GoldenUnifiedDBPath"} {
			if strings.Contains(fn, forbidden) {
				t.Fatalf("%s must exercise the real migrator; found %q", name, forbidden)
			}
		}
	}
}

func functionBody(t *testing.T, source, name string) string {
	t.Helper()
	start := strings.Index(source, "func "+name+"(")
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	next := strings.Index(source[start+len("func "):], "\nfunc ")
	if next < 0 {
		return source[start:]
	}
	return source[start : start+len("func ")+next]
}
