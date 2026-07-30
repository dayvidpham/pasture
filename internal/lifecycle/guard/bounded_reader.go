package guard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// CheckBoundedReaderSource rejects direct persistence access from lifecycle
// production-path tests. acceptance.SnapshotFile remains the single explicit
// whole-store inspection boundary.
func CheckBoundedReaderSource(path string, source []byte) []string {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return []string{fmt.Sprintf("%s cannot be parsed: %v", path, err)}
	}
	var findings []string
	for _, imp := range file.Imports {
		name, _ := strconv.Unquote(imp.Path.Value)
		if name == "database/sql" || strings.Contains(name, "/lifecycle/projection") {
			findings = append(findings, fmt.Sprintf("%s imports forbidden production storage package %q", path, name))
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		upper := strings.ToUpper(strings.TrimSpace(value))
		for _, prefix := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "CREATE TABLE", "ALTER TABLE", "PRAGMA "} {
			if strings.HasPrefix(upper, prefix) {
				findings = append(findings, fmt.Sprintf("%s contains forbidden SQL literal beginning %q", path, prefix))
				break
			}
		}
		return true
	})
	return findings
}
