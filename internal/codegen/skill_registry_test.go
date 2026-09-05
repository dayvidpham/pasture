package codegen

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

var portableSkillDirPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// TestGeneratedSkillRegistryParity keeps the three generated-skill inventories
// synchronized:
//
//   - CommandSpecs declares skill metadata and output paths.
//   - roleSkillDirs + commandSkillDirs select the skills emitted by a harness.
//   - SkillBodySpecs supplies each generated skill's hand-authored body.
//
// A new skill must appear exactly once in all three inventories. Keeping this
// check in package codegen lets it inspect the unexported harness maps directly.
func TestGeneratedSkillRegistryParity(t *testing.T) {
	metadataDirs := make(map[string]string, len(CommandSpecs))
	metadataRoleDirs := make(map[string]string, len(roleSkillDirs))
	metadataCommandDirs := make(map[string]string, len(commandSkillDirs))
	for _, commandID := range sortedCommandSpecIDs() {
		spec := CommandSpecs[commandID]
		if spec.Id != commandID {
			t.Errorf(
				"generated skill command metadata mismatch — "+
					"what: CommandSpecs map key %q contains Id %q; "+
					"why: generators and lookups address commands by the map key, so a divergent Id makes the registry ambiguous; "+
					"where: internal/codegen/specs_data.go CommandSpecs[%q]; "+
					"when: TestGeneratedSkillRegistryParity validates command metadata before generation; "+
					"what it means for the caller: codegen tests fail and generated skill ownership cannot be trusted; "+
					"fix: make CommandSpec.Id equal the map key",
				commandID, spec.Id, commandID,
			)
		}

		dir, valid := skillDirFromCommandFile(spec.File)
		if !valid {
			t.Errorf(
				"generated skill command path is invalid — "+
					"what: CommandSpecs[%q].File=%q does not identify skills/<portable-slug>/SKILL.md; "+
					"why: harness and body registries join metadata by a lowercase hyphenated directory that must be portable across supported filesystems; "+
					"where: internal/codegen/specs_data.go CommandSpecs[%q]; "+
					"when: TestGeneratedSkillRegistryParity derives the skill directory from command metadata; "+
					"what it means for the caller: codegen tests fail because harness/body parity cannot be established; "+
					"fix: set File to skills/<lowercase-hyphenated-skill-dir>/SKILL.md and avoid dot segments or reserved device names",
				commandID, spec.File, commandID,
			)
			continue
		}
		if previous, exists := metadataDirs[dir]; exists {
			t.Errorf(
				"generated skill command directory is duplicated — "+
					"what: CommandSpecs entries %q and %q both target skill directory %q; "+
					"why: validated directory slugs are lowercase, so exact uniqueness also prevents case-insensitive filesystem collisions; "+
					"where: internal/codegen/specs_data.go CommandSpecs; "+
					"when: TestGeneratedSkillRegistryParity indexes command metadata by output directory; "+
					"what it means for the caller: codegen tests fail because output ownership is ambiguous; "+
					"fix: give each generated command a unique skills/<dir>/SKILL.md path",
				previous, commandID, dir,
			)
			continue
		}
		metadataDirs[dir] = commandID
		if spec.RoleRef.IsValid() && dir == string(spec.RoleRef) {
			metadataRoleDirs[dir] = commandID
		} else {
			metadataCommandDirs[dir] = commandID
		}
	}

	emitterDirs := make(map[string]string, len(roleSkillDirs)+len(commandSkillDirs))
	addEmitterDir := func(dir, source string) {
		t.Helper()
		if _, valid := skillDirFromCommandFile("skills/" + dir + "/SKILL.md"); !valid {
			t.Errorf(
				"generated skill harness directory is not portable — "+
					"what: %s emits directory %q, which is not a lowercase hyphenated portable slug; "+
					"why: generated outputs must behave consistently on case-insensitive filesystems and avoid reserved device names; "+
					"where: internal/codegen/harness.go roleSkillDirs or commandSkillDirs; "+
					"when: TestGeneratedSkillRegistryParity validates harness output directories; "+
					"what it means for the caller: codegen tests fail before generation can create a non-portable path; "+
					"fix: replace the directory with a unique lowercase slug using letters, digits, and single hyphens",
				source, dir,
			)
		}
		if previous, exists := emitterDirs[dir]; exists {
			t.Errorf(
				"generated skill harness directory is duplicated — "+
					"what: %s and %s both emit skill directory %q; "+
					"why: duplicate emitters can overwrite the same generated file; "+
					"where: internal/codegen/harness.go roleSkillDirs or commandSkillDirs; "+
					"when: TestGeneratedSkillRegistryParity combines the role and command emitter registries; "+
					"what it means for the caller: codegen tests fail and generation could overwrite one skill with another; "+
					"fix: keep exactly one harness owner for each skill directory",
				previous, source, dir,
			)
			return
		}
		emitterDirs[dir] = source
	}

	roleEmitterDirs := make(map[string]string, len(roleSkillDirs))
	for _, item := range roleSkillItems() {
		roleID, dir := item.role, item.dir
		source := fmt.Sprintf("roleSkillDirs[%q]", roleID)
		addEmitterDir(dir, source)
		roleEmitterDirs[dir] = source
		if dir != string(roleID) {
			t.Errorf(
				"generated role skill directory disagrees with its body lookup key — "+
					"what: %s maps role %q to directory %q, but role bodies are addressed by role ID %q; "+
					"why: GenerateSkill looks up SkillBodySpecs[string(roleID)], so a different directory can pass set parity while rendering no registered body; "+
					"where: internal/codegen/harness.go roleSkillDirs and internal/codegen/skills.go renderSkill; "+
					"when: TestGeneratedSkillRegistryParity validates each role emitter before generation; "+
					"what it means for the caller: codegen tests fail because the emitted role skill would silently omit its body; "+
					"fix: use the role ID as the role skill directory and SkillBodySpecs key",
				source, roleID, dir, roleID,
			)
		}

		matchingCommands := make([]string, 0, 1)
		for commandID, spec := range CommandSpecs {
			if spec.RoleRef == roleID && spec.File == "skills/"+dir+"/SKILL.md" {
				matchingCommands = append(matchingCommands, commandID)
			}
		}
		sort.Strings(matchingCommands)
		if len(matchingCommands) != 1 {
			t.Errorf(
				"generated role skill has no unique command metadata owner — "+
					"what: %s emits %q but matching CommandSpecs entries are %v; "+
					"why: role generation, command metadata, and body lookup must describe the same skill; "+
					"where: internal/codegen/harness.go roleSkillDirs and internal/codegen/specs_data.go CommandSpecs; "+
					"when: TestGeneratedSkillRegistryParity links a role emitter to its command metadata; "+
					"what it means for the caller: codegen tests fail because the role skill cannot be proven complete; "+
					"fix: add or correct exactly one CommandSpec with RoleRef=%q and File=%q",
				source, dir, matchingCommands, roleID, "skills/"+dir+"/SKILL.md",
			)
		}
	}

	commandEmitterDirs := make(map[string]string, len(commandSkillDirs))
	for _, item := range commandSkillItems() {
		commandID, dir := item.commandID, item.dir
		source := fmt.Sprintf("commandSkillDirs[%q]", commandID)
		addEmitterDir(dir, source)
		commandEmitterDirs[dir] = source

		spec, exists := CommandSpecs[commandID]
		if !exists {
			t.Errorf(
				"generated command skill has no metadata — "+
					"what: %s emits %q but CommandSpecs has no %q entry; "+
					"why: GenerateSubSkill requires command metadata for the emitted body and frontmatter; "+
					"where: internal/codegen/harness.go commandSkillDirs; "+
					"when: TestGeneratedSkillRegistryParity resolves a command emitter before generation; "+
					"what it means for the caller: codegen tests fail and GenerateSubSkill would have no metadata to render; "+
					"fix: add CommandSpecs[%q] or remove the orphaned emitter entry",
				source, dir, commandID, commandID,
			)
			continue
		}
		if strings.TrimSpace(spec.Title) == "" {
			t.Errorf(
				"generated command skill has no curated title — "+
					"what: CommandSpecs[%q] is emitted as command skill %q but Title is empty; "+
					"why: GenerateSubSkill uses CommandSpec.Title as the generated H1; "+
					"where: internal/codegen/specs_data.go CommandSpecs[%q] and internal/codegen/harness.go commandSkillDirs; "+
					"when: TestGeneratedSkillRegistryParity validates command emitters before generation; "+
					"what it means for the caller: codegen tests fail because the skill would render without its required curated heading; "+
					"fix: set CommandSpecs[%q].Title to the command skill's H1 text",
				commandID, dir, commandID, commandID,
			)
		}
		metadataDir, valid := skillDirFromCommandFile(spec.File)
		if !valid {
			continue // The metadata loop reports the actionable path error.
		}
		if metadataDir != dir {
			t.Errorf(
				"generated command skill directory disagrees with its metadata — "+
					"what: %s emits %q while CommandSpecs[%q].File resolves to %q; "+
					"why: the harness would write one directory while body/frontmatter lookup addresses another; "+
					"where: internal/codegen/harness.go commandSkillDirs and internal/codegen/specs_data.go CommandSpecs; "+
					"when: TestGeneratedSkillRegistryParity compares an emitter directory with CommandSpec.File; "+
					"what it means for the caller: codegen tests fail and generation/body lookup would target different skills; "+
					"fix: make commandSkillDirs[%q] match the directory in CommandSpecs[%q].File",
				source, dir, commandID, metadataDir, commandID, commandID,
			)
		}
	}

	bodyDirs := make(map[string]string, len(SkillBodySpecs))
	for _, dir := range sortedStringKeys(SkillBodySpecs) {
		if _, valid := skillDirFromCommandFile("skills/" + dir + "/SKILL.md"); !valid {
			t.Errorf(
				"generated skill body key is not portable — "+
					"what: SkillBodySpecs key %q is not a lowercase hyphenated portable directory slug; "+
					"why: body keys join metadata and harness output paths across supported filesystems; "+
					"where: internal/codegen/specs_data_body.go SkillBodySpecs; "+
					"when: TestGeneratedSkillRegistryParity validates body-registry keys; "+
					"what it means for the caller: codegen tests fail because body lookup would not be filesystem-portable; "+
					"fix: rename the key and matching output directory to a unique lowercase slug using letters, digits, and single hyphens",
				dir,
			)
		}
		bodyDirs[dir] = fmt.Sprintf("SkillBodySpecs[%q]", dir)
	}

	assertSkillDirSetEqual(t, "CommandSpecs", metadataDirs, "harness emitters", emitterDirs)
	assertSkillDirSetEqual(t, "CommandSpecs", metadataDirs, "SkillBodySpecs", bodyDirs)
	assertSkillDirSetEqual(t, "role-level CommandSpecs", metadataRoleDirs, "roleSkillDirs", roleEmitterDirs)
	assertSkillDirSetEqual(t, "command-level CommandSpecs", metadataCommandDirs, "commandSkillDirs", commandEmitterDirs)
}

// TestSchemaRegistryParity keeps schema emission aligned with the typed protocol
// and generated-skill inventories. Role order comes directly from AllRoleIds;
// the remaining explicit ordering/step maps must contain the same keys as their
// canonical registries.
func TestSchemaRegistryParity(t *testing.T) {
	commandIDs := make(map[string]string, len(CommandSpecs))
	for commandID := range CommandSpecs {
		commandIDs[commandID] = "CommandSpecs"
	}
	orderedCommandIDs := make(map[string]string, len(commandOrder))
	for _, commandID := range commandOrder {
		if _, duplicate := orderedCommandIDs[commandID]; duplicate {
			t.Errorf(
				"schema command order contains a duplicate — "+
					"what: commandOrder lists %q more than once; "+
					"why: schema.xml command emission is driven by this ordered list; "+
					"where: internal/codegen/schema.go commandOrder; "+
					"when: TestSchemaRegistryParity indexes the schema command inventory; "+
					"what it means for the caller: codegen tests fail because schema.xml would emit ambiguous duplicate commands; "+
					"fix: keep each CommandSpecs key exactly once in commandOrder",
				commandID,
			)
		}
		orderedCommandIDs[commandID] = "commandOrder"
	}
	assertRegistrySetEqual(
		t,
		"CommandSpecs", commandIDs,
		"schema commandOrder", orderedCommandIDs,
		"internal/codegen/specs_data.go CommandSpecs and internal/codegen/schema.go commandOrder",
		"add or remove the named command in commandOrder so schema.xml emits every CommandSpec exactly once",
	)

	roleIDs := make(map[string]string, len(protocol.AllRoleIds))
	for _, roleID := range protocol.AllRoleIds {
		roleIDs[string(roleID)] = "protocol.AllRoleIds"
	}
	procedureRoleIDs := make(map[string]string, len(ProcedureSteps))
	for roleID := range ProcedureSteps {
		procedureRoleIDs[string(roleID)] = "ProcedureSteps"
	}
	assertRegistrySetEqual(
		t,
		"protocol.AllRoleIds", roleIDs,
		"ProcedureSteps", procedureRoleIDs,
		"pkg/protocol/enums.go AllRoleIds and internal/codegen/specs_data.go ProcedureSteps",
		"add an empty or populated ProcedureSteps entry for every role and remove orphaned role keys",
	)

	var emitted struct {
		Roles struct {
			Items []struct {
				ID string `xml:"id,attr"`
			} `xml:"role"`
		} `xml:"roles"`
		Commands struct {
			Items []struct {
				ID string `xml:"id,attr"`
			} `xml:"command"`
		} `xml:"commands"`
		ProcedureSteps struct {
			Items []struct {
				Ref string `xml:"ref,attr"`
			} `xml:"role"`
		} `xml:"procedure-steps"`
	}
	if err := xml.Unmarshal([]byte(generateSchemaContent()), &emitted); err != nil {
		t.Fatalf(
			"generated schema inventory could not be parsed — "+
				"what: encoding/xml rejected generateSchemaContent output: %v; "+
				"why: registry parity must compare canonical inputs with the IDs actually emitted into schema.xml; "+
				"where: internal/codegen/skill_registry_test.go TestSchemaRegistryParity; "+
				"when: the test decodes the in-memory generated schema; "+
				"what it means for the caller: emitted role/command/procedure completeness cannot be established and the test stops; "+
				"fix: correct the schema builder's XML output, then rerun the codegen tests",
			err,
		)
	}

	emittedRoles := schemaIDInventory(t, "roles/role@id", func() []string {
		ids := make([]string, 0, len(emitted.Roles.Items))
		for _, item := range emitted.Roles.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}())
	emittedCommands := schemaIDInventory(t, "commands/command@id", func() []string {
		ids := make([]string, 0, len(emitted.Commands.Items))
		for _, item := range emitted.Commands.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}())
	expectedProcedureRoles := make(map[string]string)
	for roleID, steps := range ProcedureSteps {
		if len(steps) > 0 {
			expectedProcedureRoles[string(roleID)] = "non-empty ProcedureSteps"
		}
	}
	emittedProcedureRoles := schemaIDInventory(t, "procedure-steps/role@ref", func() []string {
		ids := make([]string, 0, len(emitted.ProcedureSteps.Items))
		for _, item := range emitted.ProcedureSteps.Items {
			ids = append(ids, item.Ref)
		}
		return ids
	}())

	assertRegistrySetEqual(
		t,
		"protocol.AllRoleIds", roleIDs,
		"generated schema roles", emittedRoles,
		"internal/codegen/schema.go buildRoles and generated <roles>",
		"make buildRoles emit every canonical role exactly once",
	)
	assertRegistrySetEqual(
		t,
		"CommandSpecs", commandIDs,
		"generated schema commands", emittedCommands,
		"internal/codegen/schema.go buildCommands and generated <commands>",
		"make buildCommands emit every CommandSpec exactly once",
	)
	assertRegistrySetEqual(
		t,
		"non-empty ProcedureSteps", expectedProcedureRoles,
		"generated schema procedure roles", emittedProcedureRoles,
		"internal/codegen/schema.go buildProcedureSteps and generated <procedure-steps>",
		"emit each role with non-empty ProcedureSteps exactly once and omit only empty entries",
	)
}

// TestGeneratedOutputInventory proves both sides of the generation contract.
//
// The EXPECTED file set of each harness is DERIVED from its emitters: the
// production dry run (EmitHarness with Write false) names every file the
// harness generates, so an output added to an emitter is covered here without
// an edit to this file. Non-vacuity: every harness emits at least one file, and
// its activation report path constant is in the set. Every emitted file must be
// committed byte-identical (a missing or stale file is RED), every committed
// file under a GENERATED TREE must be emitted (a stray file is RED, orphaned),
// and every emitted path must fall under a generated tree or in a declared
// MIXED directory, so a new output tree cannot escape the walk. What the guard
// does NOT see: a stray file in a mixed directory (hooks/ carries hand-authored
// payload files beside the two emitted ones; the repository root carries
// everything), which is checked for the emitted files' presence and content
// only. The registry-derived skill and agent expectations are a second
// derivation of the same outputs, kept so a registry entry and an emitter
// cannot disagree silently. Root outputs are recognized by content so a renamed
// stale copy is still orphaned.
func TestGeneratedOutputInventory(t *testing.T) {
	root := testModuleRoot(t)

	expectedClaudeSkills := make(map[string]string, len(CommandSpecs))
	expectedOpenCodeSkills := make(map[string]string, len(CommandSpecs))
	expectedCodexSkills := make(map[string]string, len(CommandSpecs))
	expectedClaudeAgents := make(map[string]string)
	expectedOpenCodeAgents := make(map[string]string)
	expectedCodexAgents := make(map[string]string)
	expectedRootOutputs := make(map[string]string, 2)

	for _, commandID := range sortedCommandSpecIDs() {
		spec := CommandSpecs[commandID]
		dir, valid := skillDirFromCommandFile(spec.File)
		if !valid {
			continue // TestGeneratedSkillRegistryParity reports the actionable path error.
		}
		owner := "CommandSpecs[" + commandID + "]"
		addExpectedOutput(t, expectedClaudeSkills, filepath.ToSlash(spec.File), owner)
		addExpectedOutput(t, expectedOpenCodeSkills, filepath.ToSlash(filepath.Join(".opencode", "skill", dir, "SKILL.md")), owner)
		addExpectedOutput(t, expectedCodexSkills, filepath.ToSlash(filepath.Join(codexSkillRoot, dir, "SKILL.md")), owner)
	}

	for _, dir := range openCodeVerbatimDirs {
		sourceRoot := filepath.Join(root, "skills", dir)
		// The verbatim sources are hand-authored residents of the Claude skill
		// root, not Claude outputs: the orphan walk admits them by directory.
		for _, sourcePath := range sortedStringKeys(collectRelativeFiles(t, root, sourceRoot)) {
			relWithinSource, err := filepath.Rel(filepath.Join("skills", dir), filepath.FromSlash(sourcePath))
			if err != nil {
				t.Fatalf(
					"generated verbatim output path derivation failed — "+
						"what: filepath.Rel could not derive a path for source %q under skills/%s: %v; "+
						"why: the OpenCode destination inventory mirrors each registered verbatim source tree; "+
						"where: internal/codegen/skill_registry_test.go TestGeneratedOutputInventory; "+
						"when: the test translates a Claude Code source path into its OpenCode destination; "+
						"what it means for the caller: output-inventory parity cannot be checked and the test stops; "+
						"fix: ensure openCodeVerbatimDirs names a directory under skills/ and its collected paths remain repository-relative",
					sourcePath, dir, err,
				)
			}
			addExpectedOutput(t, expectedOpenCodeSkills, filepath.ToSlash(filepath.Join(".opencode", "skill", dir, relWithinSource)), "openCodeVerbatimDirs["+dir+"] destination")
			addExpectedOutput(t, expectedCodexSkills, filepath.ToSlash(filepath.Join(codexSkillRoot, dir, relWithinSource)), "codexVerbatimDirs["+dir+"] destination")
		}
	}

	for _, roleID := range protocol.AllRoleIds {
		spec := RoleSpecs[roleID]
		if len(spec.Tools) == 0 {
			continue
		}
		owner := "RoleSpecs[" + string(roleID) + "].Tools"
		addExpectedOutput(t, expectedClaudeAgents, filepath.ToSlash(filepath.Join("agents", string(roleID)+".md")), owner)
		addExpectedOutput(t, expectedOpenCodeAgents, filepath.ToSlash(filepath.Join(".opencode", "agent", string(roleID)+".md")), owner)
		addExpectedOutput(t, expectedCodexAgents, filepath.ToSlash(filepath.Join(codexAgentsRoot, "pasture-"+string(roleID)+".toml")), owner)
	}
	addExpectedOutput(t, expectedRootOutputs, "schema.xml", "GenerateSchemaToFile")
	addExpectedOutput(t, expectedRootOutputs, "opencode.json", "OpenCodeTarget.Manifest")

	for _, inventory := range generatedOutputInventories() {
		files, emitted := collectHarnessOutputs(t, root, inventory.harness)
		assertEmittedInventoryIsNonVacuous(t, inventory, emitted)
		assertGeneratedFilesCommitted(t, root, files)
		assertEmittedPathsStayInsideDeclaredOutputs(t, inventory, emitted)
		assertGeneratedTreesCarryNoOrphans(t, root, inventory, emitted)
	}

	// Registry-derived expectations against the emitters: the two derivations
	// of the skill and agent outputs must agree exactly.
	_, claudeEmitted := collectHarnessOutputs(t, root, ClaudeCodeTarget)
	_, openCodeEmitted := collectHarnessOutputs(t, root, OpenCodeTarget)
	_, codexEmitted := collectHarnessOutputs(t, root, CodexTarget)
	assertOutputSetEqual(t, "Claude Code skill outputs", expectedClaudeSkills, subsetUnder(claudeEmitted, ClaudeCodeTarget.SkillRoot))
	assertOutputSetEqual(t, "OpenCode skill outputs", expectedOpenCodeSkills, subsetUnder(openCodeEmitted, OpenCodeTarget.SkillRoot))
	assertOutputSetEqual(t, "Codex skill outputs", expectedCodexSkills, subsetUnder(codexEmitted, codexSkillRoot))
	assertOutputSetEqual(t, "Claude Code agent outputs", expectedClaudeAgents, subsetUnder(claudeEmitted, "agents"))
	assertOutputSetEqual(t, "OpenCode agent outputs", expectedOpenCodeAgents, subsetUnder(openCodeEmitted, filepath.ToSlash(filepath.Join(".opencode", "agent"))))
	assertOutputSetEqual(t, "Codex agent outputs", expectedCodexAgents, subsetUnder(codexEmitted, codexAgentsRoot))
	assertOutputSetEqual(t, "generated repository-root files", expectedRootOutputs, collectGeneratedRootFiles(t, root))

	assertCommittedContent(t, filepath.Join(root, "schema.xml"), generateSchemaContent(), "GenerateSchemaToFile")
}

// generatedOutputInventory declares, per harness, WHERE the guard looks: the
// trees whose every committed file must be emitted, the mixed directories whose
// emitted files are checked for presence and content only, the hand-authored
// source directories that live inside a generated tree, and the one path the
// emitted set must always contain. The FILE population is never listed here; it
// comes from the emitter.
type generatedOutputInventory struct {
	harness          TargetHarness
	generatedTrees   []string
	mixedDirectories []string
	sourceResidents  []string
	requiredOutput   string
}

func generatedOutputInventories() []generatedOutputInventory {
	// The verbatim skill sources are hand-authored directories below the Claude
	// skill root; every other harness copies them, Claude reads them in place.
	sources := make([]string, 0, len(openCodeVerbatimDirs)+len(codexVerbatimDirs))
	for _, dir := range append(append([]string(nil), openCodeVerbatimDirs...), codexVerbatimDirs...) {
		sources = append(sources, filepath.ToSlash(filepath.Join(ClaudeCodeTarget.SkillRoot, dir)))
	}
	return []generatedOutputInventory{
		{harness: ClaudeCodeTarget, generatedTrees: []string{ClaudeCodeTarget.SkillRoot, "agents"}, mixedDirectories: []string{"hooks"}, sourceResidents: sources, requiredOutput: ClaudeActivationReportPath},
		{harness: OpenCodeTarget, generatedTrees: []string{".opencode"}, mixedDirectories: []string{"."}, requiredOutput: OpenCodeActivationReportPath},
		{harness: CodexTarget, generatedTrees: []string{codexSkillRoot, ".codex"}, requiredOutput: codexActivationReportRelPath},
	}
}

func assertEmittedInventoryIsNonVacuous(t *testing.T, inventory generatedOutputInventory, emitted map[string]string) {
	t.Helper()
	if len(emitted) == 0 {
		t.Fatalf("the %s harness emits no file: the inventory guard has nothing to hold and would pass vacuously", inventory.harness.Name)
	}
	if _, ok := emitted[filepath.ToSlash(inventory.requiredOutput)]; !ok {
		t.Fatalf("the %s harness emitted set lacks its activation report %q; the guard's population is not the harness's real output", inventory.harness.Name, inventory.requiredOutput)
	}
}

func assertEmittedPathsStayInsideDeclaredOutputs(t *testing.T, inventory generatedOutputInventory, emitted map[string]string) {
	t.Helper()
	for _, path := range sortedStringKeys(emitted) {
		if isUnderAny(path, inventory.generatedTrees) || isDirectlyInAny(path, inventory.mixedDirectories) {
			continue
		}
		t.Errorf("the %s harness emits %q outside every declared generated tree %v and mixed directory %v; declare where it lives or the orphan walk never covers it", inventory.harness.Name, path, inventory.generatedTrees, inventory.mixedDirectories)
	}
}

func assertGeneratedTreesCarryNoOrphans(t *testing.T, root string, inventory generatedOutputInventory, emitted map[string]string) {
	t.Helper()
	for _, tree := range inventory.generatedTrees {
		committed := collectRelativeFiles(t, root, filepath.Join(root, filepath.FromSlash(tree)))
		emittedInTree := 0
		var orphaned []string
		for _, path := range sortedStringKeys(committed) {
			if _, ok := emitted[path]; ok {
				emittedInTree++
				continue
			}
			if isUnderAny(path, inventory.sourceResidents) {
				continue
			}
			orphaned = append(orphaned, path)
		}
		if emittedInTree == 0 {
			t.Errorf("the %s harness emits nothing under its declared generated tree %q; the tree is misdeclared or the emitter is empty", inventory.harness.Name, tree)
		}
		if len(orphaned) > 0 {
			t.Errorf("generated tree %q of the %s harness holds committed files no emitter produces (orphaned: %v); in-place generation cannot remove a retired output, so delete each one after confirming its source was retired", tree, inventory.harness.Name, orphaned)
		}
	}
}

func subsetUnder(paths map[string]string, tree string) map[string]string {
	out := make(map[string]string)
	for path, owner := range paths {
		if isUnderAny(path, []string{tree}) {
			out[path] = owner
		}
	}
	return out
}

func isUnderAny(path string, trees []string) bool {
	for _, tree := range trees {
		tree = filepath.ToSlash(tree)
		if strings.HasPrefix(path, tree+"/") {
			return true
		}
	}
	return false
}

func isDirectlyInAny(path string, dirs []string) bool {
	for _, dir := range dirs {
		if filepath.ToSlash(filepath.Dir(path)) == filepath.ToSlash(dir) {
			return true
		}
	}
	return false
}

func skillDirFromCommandFile(file string) (string, bool) {
	parts := strings.Split(file, "/")
	if len(parts) != 3 || parts[0] != "skills" || parts[1] == "" || parts[2] != "SKILL.md" {
		return "", false
	}
	dir := parts[1]
	if !portableSkillDirPattern.MatchString(dir) || isReservedPortableName(dir) {
		return "", false
	}
	return dir, true
}

func TestSkillDirFromCommandFileRequiresPortableSlug(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file string
		dir  string
		ok   bool
	}{
		{file: "skills/worker-implement/SKILL.md", dir: "worker-implement", ok: true},
		{file: "skills/v2-review/SKILL.md", dir: "v2-review", ok: true},
		{file: "skills/../SKILL.md", ok: false},
		{file: "skills/Worker/SKILL.md", ok: false},
		{file: "skills/worker_review/SKILL.md", ok: false},
		{file: "skills/con/SKILL.md", ok: false},
		{file: "skills/worker/SKILL.txt", ok: false},
		{file: "other/worker/SKILL.md", ok: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.file, func(t *testing.T) {
			t.Parallel()
			dir, ok := skillDirFromCommandFile(test.file)
			if dir != test.dir || ok != test.ok {
				t.Errorf(
					"portable skill-directory validation returned an unexpected result — "+
						"what: skillDirFromCommandFile(%q) = (%q, %t), want (%q, %t); "+
						"why: generated skill paths must remain lowercase, traversal-free, and portable across supported filesystems; "+
						"where: internal/codegen/skill_registry_test.go skillDirFromCommandFile; "+
						"when: TestSkillDirFromCommandFileRequiresPortableSlug exercises valid and invalid paths; "+
						"what it means for the caller: the path guard accepted an unsafe path or rejected a supported slug; "+
						"fix: correct the slug validation rules without weakening traversal, case, or reserved-name protection",
					test.file, dir, ok, test.dir, test.ok,
				)
			}
		})
	}
}

func TestGeneratedRootInventoryRecognizesRenamedStaleCopies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"retired-schema.xml":    `<?xml version="1.0"?><pasture-protocol version="2.0"></pasture-protocol>`,
		"retired-manifest.json": `{"$schema":"https://opencode.ai/config.json","skills":{"paths":[".opencode/skill"]}}`,
		"ordinary.json":         `{"$schema":"https://example.com/other.json"}`,
		"README.md":             `The text <pasture-protocol version= is not an XML document.`,
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf(
				"generated-root inventory fixture could not be written — "+
					"what: os.WriteFile(%q) failed: %v; "+
					"why: the test needs renamed schema/manifest fixtures to prove signature-based orphan detection; "+
					"where: internal/codegen/skill_registry_test.go TestGeneratedRootInventoryRecognizesRenamedStaleCopies; "+
					"when: arranging the temporary repository root; "+
					"what it means for the caller: the signature guard cannot be exercised and the test stops; "+
					"fix: restore writable temporary-directory access and rerun the test",
				path, err,
			)
		}
	}

	got := collectGeneratedRootFiles(t, root)
	want := map[string]string{
		"retired-schema.xml":    "schema signature",
		"retired-manifest.json": "manifest signature",
	}
	assertOutputSetEqual(t, "renamed generated-root fixture files", want, got)
}

func assertSkillDirSetEqual(
	t *testing.T,
	expectedName string,
	expected map[string]string,
	actualName string,
	actual map[string]string,
) {
	t.Helper()

	missing := make([]string, 0)
	for dir := range expected {
		if _, exists := actual[dir]; !exists {
			missing = append(missing, dir)
		}
	}
	sort.Strings(missing)

	orphaned := make([]string, 0)
	for dir := range actual {
		if _, exists := expected[dir]; !exists {
			orphaned = append(orphaned, dir)
		}
	}
	sort.Strings(orphaned)

	if len(missing) == 0 && len(orphaned) == 0 {
		return
	}
	t.Errorf(
		"generated skill registry parity failed — "+
			"what: %s and %s differ (missing from %s: %v; orphaned in %s: %v); "+
			"why: every generated skill needs one metadata entry, one harness emitter, and one body; "+
			"where: internal/codegen/specs_data.go CommandSpecs, internal/codegen/harness.go registries, and internal/codegen/specs_data_body.go SkillBodySpecs; "+
			"when: TestGeneratedSkillRegistryParity compares the complete generated-skill directory sets; "+
			"what it means for the caller: codegen tests fail and a named skill would be skipped or left orphaned during generation; "+
			"fix: add or remove the named skill directory in the out-of-sync registry",
		expectedName, actualName, actualName, missing, actualName, orphaned,
	)
}

func assertRegistrySetEqual(
	t *testing.T,
	expectedName string,
	expected map[string]string,
	actualName string,
	actual map[string]string,
	where string,
	fix string,
) {
	t.Helper()
	missing, orphaned := registrySetDiff(expected, actual)
	if len(missing) == 0 && len(orphaned) == 0 {
		return
	}
	t.Errorf(
		"registry parity failed — "+
			"what: %s and %s differ (missing from %s: %v; orphaned in %s: %v); "+
			"why: every canonical registry entry must be emitted exactly once by its ordered/output inventory; "+
			"where: %s; "+
			"when: TestSchemaRegistryParity compares complete registry key sets; "+
			"what it means for the caller: codegen tests fail because generated schema or runtime outputs would be incomplete or orphaned; "+
			"fix: %s",
		expectedName, actualName, actualName, missing, actualName, orphaned, where, fix,
	)
}

func assertOutputSetEqual(t *testing.T, outputName string, expected, actual map[string]string) {
	t.Helper()
	missing, orphaned := registrySetDiff(expected, actual)
	if len(missing) == 0 && len(orphaned) == 0 {
		return
	}
	t.Errorf(
		"generated output inventory mismatch — "+
			"what: %s differ from the canonical inventory (missing: %v; orphaned: %v); "+
			"why: in-place generation writes current outputs but cannot remove files retired from a registry or verbatim source; "+
			"where: repository output trees selected by internal/codegen/harness.go; "+
			"when: TestGeneratedOutputInventory compares the checked-out file set after generation; "+
			"what it means for the caller: codegen tests fail because stale runtime files could remain committed even when regeneration is otherwise clean; "+
			"fix: run make generate, add missing outputs, and explicitly remove each orphaned generated file after confirming its registry/source entry was intentionally retired",
		outputName, missing, orphaned,
	)
}

func registrySetDiff(expected, actual map[string]string) (missing, orphaned []string) {
	for key := range expected {
		if _, exists := actual[key]; !exists {
			missing = append(missing, key)
		}
	}
	for key := range actual {
		if _, exists := expected[key]; !exists {
			orphaned = append(orphaned, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(orphaned)
	return missing, orphaned
}

func sortedCommandSpecIDs() []string {
	return sortedStringKeys(CommandSpecs)
}

func isReservedPortableName(dir string) bool {
	switch strings.ToLower(dir) {
	case "con", "prn", "aux", "nul",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return true
	default:
		return false
	}
}

func schemaIDInventory(t *testing.T, location string, ids []string) map[string]string {
	t.Helper()
	inventory := make(map[string]string, len(ids))
	for index, id := range ids {
		if strings.TrimSpace(id) == "" {
			t.Errorf(
				"generated schema contains an empty inventory identifier — "+
					"what: %s item %d has no ID/ref value; "+
					"why: exact emitted-registry parity addresses every element by that identifier; "+
					"where: internal/codegen/schema.go generated %s; "+
					"when: TestSchemaRegistryParity decodes generated schema.xml; "+
					"what it means for the caller: codegen tests fail because the emitted element cannot be matched to its canonical spec; "+
					"fix: make the relevant schema builder populate the element's ID/ref attribute",
				location, index, location,
			)
			continue
		}
		if previous, exists := inventory[id]; exists {
			t.Errorf(
				"generated schema contains a duplicate inventory identifier — "+
					"what: %s emits %q at both %s and item %d; "+
					"why: one canonical registry entry must produce exactly one schema element; "+
					"where: internal/codegen/schema.go generated %s; "+
					"when: TestSchemaRegistryParity indexes emitted identifiers; "+
					"what it means for the caller: codegen tests fail because schema consumers would see ambiguous duplicates; "+
					"fix: remove the duplicate builder input or duplicate emission branch",
				location, id, previous, index, location,
			)
			continue
		}
		inventory[id] = fmt.Sprintf("%s item %d", location, index)
	}
	return inventory
}

func addExpectedOutput(t *testing.T, inventory map[string]string, path, owner string) {
	t.Helper()
	if previous, exists := inventory[path]; exists {
		t.Errorf(
			"generated output path has multiple owners — "+
				"what: %q is claimed by both %s and %s; "+
				"why: one committed path cannot safely represent two registry entries or emitters; "+
				"where: canonical output inventories assembled in TestGeneratedOutputInventory; "+
				"when: the test derives expected or actual harness paths; "+
				"what it means for the caller: codegen tests fail before one generator can overwrite another's output; "+
				"fix: assign each skill, agent, verbatim source, manifest, and schema a unique portable output path",
			path, previous, owner,
		)
		return
	}
	inventory[path] = owner
}

func collectHarnessOutputs(
	t *testing.T,
	repoRoot string,
	harness TargetHarness,
) ([]GeneratedFile, map[string]string) {
	t.Helper()
	files, err := EmitHarness(RepoRoots(repoRoot), harness, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf(
			"harness output inventory could not be rendered — "+
				"what: EmitHarness(%s) failed: %v; "+
				"why: exact path parity compares registry-derived expectations with the production harness result; "+
				"where: internal/codegen/harness.go EmitHarness; "+
				"when: TestGeneratedOutputInventory renders the selected harness without writing; "+
				"what it means for the caller: emitted-path and committed-content parity cannot be established and the test stops; "+
				"fix: correct the named harness emitter or its canonical registry inputs, then rerun the codegen tests",
			harness.Name, err,
		)
	}
	paths := make(map[string]string, len(files))
	for index, file := range files {
		rel, err := filepath.Rel(repoRoot, file.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Errorf(
				"harness emitted a path outside the repository — "+
					"what: EmitHarness(%s) output %d has path %q, which cannot be made relative to %q: %v; "+
					"why: committed output inventory is scoped to this repository root; "+
					"where: internal/codegen/harness.go EmitHarness; "+
					"when: TestGeneratedOutputInventory normalizes returned GeneratedFile paths; "+
					"what it means for the caller: codegen tests fail because generation could escape the repository; "+
					"fix: make every target root and emitter path resolve beneath the module root",
				harness.Name, index, file.Path, repoRoot, err,
			)
			continue
		}
		addExpectedOutput(t, paths, filepath.ToSlash(rel), fmt.Sprintf("EmitHarness(%s) output %d", harness.Name, index))
	}
	return files, paths
}

func assertGeneratedFilesCommitted(t *testing.T, repoRoot string, files []GeneratedFile) {
	t.Helper()
	sorted := append([]GeneratedFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, file := range sorted {
		rel, err := filepath.Rel(repoRoot, file.Path)
		if err != nil {
			rel = file.Path
		}
		assertCommittedContent(t, file.Path, file.Content, "EmitHarness output "+filepath.ToSlash(rel))
	}
}

func assertCommittedContent(t *testing.T, path, expected, owner string) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Errorf(
			"generated output is not committed and readable — "+
				"what: %s expects %q, but reading it failed: %v; "+
				"why: every production emitter result must have a checked-in byte-identical baseline; "+
				"where: repository generated outputs; "+
				"when: TestGeneratedOutputInventory compares rendered content with disk; "+
				"what it means for the caller: codegen tests fail because the runtime artifact is missing or unreadable; "+
				"fix: run make generate, review the output, and commit the missing file",
			owner, path, err,
		)
		return
	}
	if string(actual) != expected {
		t.Errorf(
			"committed generated output has drifted — "+
				"what: %s renders content different from %q; "+
				"why: source registries/templates and the committed runtime artifact are out of sync; "+
				"where: %s; "+
				"when: TestGeneratedOutputInventory performs byte-for-byte comparison; "+
				"what it means for the caller: codegen tests fail and consumers would run stale output; "+
				"fix: run make generate, inspect the diff, and commit the regenerated file",
			owner, path, path,
		)
	}
}

func collectGeneratedRootFiles(t *testing.T, repoRoot string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		t.Fatalf(
			"repository-root generated output scan failed — "+
				"what: os.ReadDir could not list %q: %v; "+
				"why: stale renamed schema or manifest files are detected by scanning root-file signatures; "+
				"where: internal/codegen/skill_registry_test.go collectGeneratedRootFiles; "+
				"when: TestGeneratedOutputInventory inventories committed root outputs; "+
				"what it means for the caller: orphaned root outputs cannot be detected and the test stops; "+
				"fix: restore read access to the repository root and rerun the codegen tests",
			repoRoot, err,
		)
	}
	files := make(map[string]string)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(repoRoot, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf(
				"repository-root generated output signature read failed — "+
					"what: os.ReadFile could not read %q: %v; "+
					"why: schema/manifest signatures distinguish generated root files from ordinary repository files; "+
					"where: internal/codegen/skill_registry_test.go collectGeneratedRootFiles; "+
					"when: TestGeneratedOutputInventory scans repository-root files; "+
					"what it means for the caller: the root output set cannot be proven exact and the test stops; "+
					"fix: restore read access to the named file or remove the unreadable filesystem entry",
				path, err,
			)
		}
		var schemaDocument struct {
			XMLName xml.Name
		}
		if xml.Unmarshal(content, &schemaDocument) == nil && schemaDocument.XMLName.Local == "pasture-protocol" {
			addExpectedOutput(t, files, entry.Name(), "Pasture protocol XML signature")
			continue
		}
		var manifest struct {
			Schema string `json:"$schema"`
			Skills struct {
				Paths []string `json:"paths"`
			} `json:"skills"`
		}
		if json.Unmarshal(content, &manifest) == nil &&
			manifest.Schema == "https://opencode.ai/config.json" &&
			len(manifest.Skills.Paths) > 0 {
			addExpectedOutput(t, files, entry.Name(), "OpenCode manifest JSON signature")
		}
	}
	return files
}

func collectRelativeFiles(t *testing.T, repoRoot, treeRoot string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(treeRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = treeRoot
		return nil
	})
	if err != nil {
		t.Fatalf(
			"generated output inventory scan failed — "+
				"what: filepath.WalkDir could not scan %q: %v; "+
				"why: exact output-set parity requires every checked-in file beneath that generated tree; "+
				"where: internal/codegen/skill_registry_test.go collectRelativeFiles; "+
				"when: TestGeneratedOutputInventory collects actual or verbatim-source files; "+
				"what it means for the caller: missing and orphaned generated outputs cannot be distinguished, so the test stops; "+
				"fix: restore the expected directory and readable files, or correct the registry/root path that selected it",
			treeRoot, err,
		)
	}
	return files
}
