package guard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

func ValidateKnownTimeoutProfiles() error {
	for _, profile := range timeouts.KnownProfiles() {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("timeout profile %d: %w", profile.Kind(), err)
		}
	}
	return nil
}

// CheckTimeoutSource rejects the former independent timeout definitions. The
// only allowed duration construction site is internal/timeouts/profile.go.
func CheckTimeoutSource(path string, source []byte) []string {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return []string{fmt.Sprintf("%s cannot be parsed: %v", path, err)}
	}
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			if value.Name == "DefaultIngressDeadline" {
				findings = append(findings, path+" declares or uses retired DefaultIngressDeadline")
			}
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return true
			}
			text, _ := strconv.Unquote(value.Value)
			if strings.Contains(text, "busy_timeout(5000)") {
				findings = append(findings, path+" hard-codes the retired five-second SQLite retry")
			}
		}
		return true
	})
	return findings
}
