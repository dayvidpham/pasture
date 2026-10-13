package codegen

import (
	"fmt"
	"path/filepath"
)

// GenerateResult accounts for every published file: schema.xml, harness files
// in target order, and authoring-side inventory projections separately. A
// validation failure returns an empty result because nothing was published.
type GenerateResult struct {
	SchemaPath     string
	Files          []GeneratedFile
	InventoryFiles []GeneratedFile
}

// Generate runs the complete Pasture code-generation pipeline against root
// (the module root containing skills/ and agents/):
//
//  1. The strict source-migration gate (RequireClassifiedSource). This runs
//     first and, on failure, returns immediately with no schema or harness
//     output written — an unclassified harness-syntax candidate must abort
//     generation with no partial or inconsistent output.
//  2. The strict lifecycle-row gate (RequireEvidencedLifecycleRows), which
//     refuses a pinned lifecycle row that claims a blocking exit code without
//     citing the host evidence for it. It aborts before any write for the same
//     reason.
//  3. In-memory staging of schema.xml and every requested harness target's
//     skills, agents, verbatim copies and manifest.
//  4. Global-ID uniqueness enforcement and in-memory inventory staging.
//  5. Operational validation of the complete candidate stage, then publication
//     of those exact bytes. Rejection leaves every stage output untouched.
//
// Steps 3–5 accumulate their errors so a single run reports every generation
// problem at once; the returned []error is nil only when the whole pipeline
// succeeded. The strict gate in step 1 is a hard precondition: its failure is
// returned as the sole error precisely because nothing downstream ran.
func Generate(root string, targets []TargetHarness, opts GenerateOptions) (GenerateResult, []error) {
	// ── 1. Strict source-migration gate (fail-closed, before any write) ──────
	if err := RequireClassifiedSource(root); err != nil {
		return GenerateResult{}, []error{err}
	}

	// ── 2. Strict lifecycle-row gate (fail-closed, before any write) ─────────
	if err := RequireEvidencedLifecycleRows(); err != nil {
		return GenerateResult{}, []error{err}
	}

	var result GenerateResult
	var errs []error

	// ── 3. schema.xml ────────────────────────────────────────────────────────
	schemaPath := filepath.Join(root, "schema.xml")
	content, err := GenerateSchemaToFile(schemaPath, GenerateOptions{})
	if err != nil {
		errs = append(errs, fmt.Errorf("schema: %w", err))
	} else {
		result.SchemaPath = schemaPath
	}

	// ── 4. Harness-specific skills, agents, verbatim copies, and manifests ───
	for _, target := range targets {
		files, err := EmitHarness(RepoRoots(root), target, GenerateOptions{})
		if err != nil {
			errs = append(errs, fmt.Errorf("target %s: %w", target.Name, err))
			continue
		}
		result.Files = append(result.Files, files...)
	}

	// ── 5. Global-ID uniqueness enforcement ──────────────────────────────────
	// Runs after all generators so the full registry is assembled.
	if err := ValidateGlobalIds(); err != nil {
		errs = append(errs, fmt.Errorf("global-id validation: %w", err))
	}

	// ── 6. Authoring-side inventory projections (command + native axes) ───────
	// The command-skill authoring surface and the contract-derived native call
	// names are projected into internal/inventory as committed generated rows.
	// (The lifecycle-event axis is emitted by hostcontractgen in its manifest
	// walk.) Emitted as text; no production package imports the table.
	inventory, err := renderInventoryFiles(root)
	if err != nil {
		errs = append(errs, fmt.Errorf("inventory: %w", err))
	}
	if len(errs) > 0 {
		return GenerateResult{}, errs
	}
	stage := append([]GeneratedFile{{Path: schemaPath, Content: content}}, result.Files...)
	stage = append(stage, inventory...)
	if err := ValidateOperationalFiles(root, stage, "complete generation candidate validation"); err != nil {
		return GenerateResult{}, []error{err}
	}
	if err := publishGeneratedFiles(stage, opts); err != nil {
		return GenerateResult{}, []error{err}
	}
	result.InventoryFiles = inventory

	return result, errs
}
