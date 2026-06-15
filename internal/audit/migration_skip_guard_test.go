package audit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationTestsDoNotUseSkipMigrations(t *testing.T) {
	files := []string{
		"migrate_v4_v5_test.go",
		"migrate_test.go",
		"migrate_v2_v3_test.go",
		"migrate_v3_backfill_test.go",
		"schema_meta_test.go",
	}
	for _, name := range files {
		name := name
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(".", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			body := string(data)
			for _, forbidden := range []string{"WithSkipMigrations", "OpenGoldenTaskTracker", "GoldenUnifiedDBPath"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("%s must exercise the real migrator; found %q", name, forbidden)
				}
			}
		})
	}
}
