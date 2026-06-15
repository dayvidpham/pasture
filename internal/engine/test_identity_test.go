package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
)

func testEngineIdentity(t *testing.T) (executorID, appVersion string) {
	t.Helper()
	name := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return unicode.ToLower(r)
		default:
			return '-'
		}
	}, t.Name())
	name = strings.Trim(name, "-")
	if name == "" {
		name = "unnamed"
	}
	return fmt.Sprintf("test-executor-%s", name), fmt.Sprintf("test-app-%s", name)
}
