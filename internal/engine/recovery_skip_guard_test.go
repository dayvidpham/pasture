package engine_test

import (
	"os"
	"strings"
	"testing"
)

func TestRecoveryTestsDoNotUseSkipMigrations(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("recovery_test.go")
	if err != nil {
		t.Fatalf("read recovery_test.go: %v", err)
	}
	body := string(data)
	for _, forbidden := range []string{"WithSkipMigrations", "OpenGoldenTaskTracker", "GoldenUnifiedDBPath"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("recovery_test.go must exercise the real migrator; found %q", forbidden)
		}
	}
}
