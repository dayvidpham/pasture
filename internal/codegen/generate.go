package codegen

import (
	"fmt"
	"path/filepath"
)

// GenerateResult is the outcome of a full generation run: the schema.xml path
// (empty if the schema step failed) and every harness file emitted, in target
// order.
type GenerateResult struct {
	SchemaPath string
	Files      []GeneratedFile
}

// Generate runs the complete Pasture code-generation pipeline against root
// (the module root containing skills/ and agents/):
//
//  1. The strict source-migration gate (RequireClassifiedSource). This runs
//     first and, on failure, returns immediately with no schema or harness
//     output written — an unclassified harness-syntax candidate must abort
//     generation with no partial or inconsistent output.
//  2. schema.xml generation.
//  3. Every requested harness target's skills, agents, verbatim copies, and
//     manifest.
//  4. Global-ID uniqueness enforcement across the assembled registry.
//
// Steps 2–4 accumulate their errors so a single run reports every generation
// problem at once; the returned []error is nil only when the whole pipeline
// succeeded. The strict gate in step 1 is a hard precondition: its failure is
// returned as the sole error precisely because nothing downstream ran.
func Generate(root string, targets []TargetHarness, opts GenerateOptions) (GenerateResult, []error) {
	// ── 1. Strict source-migration gate (fail-closed, before any write) ──────
	if err := RequireClassifiedSource(root); err != nil {
		return GenerateResult{}, []error{err}
	}

	var result GenerateResult
	var errs []error

	// ── 2. schema.xml ────────────────────────────────────────────────────────
	schemaPath := filepath.Join(root, "schema.xml")
	if _, err := GenerateSchemaToFile(schemaPath, opts); err != nil {
		errs = append(errs, fmt.Errorf("schema: %w", err))
	} else {
		result.SchemaPath = schemaPath
	}

	// ── 3. Harness-specific skills, agents, verbatim copies, and manifests ───
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	for _, target := range targets {
		files, err := EmitHarness(root, target, figuresDir, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("target %s: %w", target.Name, err))
			continue
		}
		result.Files = append(result.Files, files...)
	}

	// ── 4. Global-ID uniqueness enforcement ──────────────────────────────────
	// Runs after all generators so the full registry is assembled.
	if err := ValidateGlobalIds(); err != nil {
		errs = append(errs, fmt.Errorf("global-id validation: %w", err))
	}

	return result, errs
}
