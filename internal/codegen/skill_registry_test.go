package codegen

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

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
	for commandID, spec := range CommandSpecs {
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
					"what: CommandSpecs[%q].File=%q does not identify skills/<dir>/SKILL.md; "+
					"why: the harness and body registries join command metadata by that directory; "+
					"where: internal/codegen/specs_data.go CommandSpecs[%q]; "+
					"when: TestGeneratedSkillRegistryParity derives the skill directory from command metadata; "+
					"what it means for the caller: codegen tests fail because harness/body parity cannot be established; "+
					"fix: set File to skills/<skill-dir>/SKILL.md",
				commandID, spec.File, commandID,
			)
			continue
		}
		if previous, exists := metadataDirs[dir]; exists {
			t.Errorf(
				"generated skill command directory is duplicated — "+
					"what: CommandSpecs entries %q and %q both target skill directory %q; "+
					"why: one generated output cannot have two metadata owners; "+
					"where: internal/codegen/specs_data.go CommandSpecs; "+
					"when: TestGeneratedSkillRegistryParity indexes command metadata by output directory; "+
					"what it means for the caller: codegen tests fail because output ownership is ambiguous; "+
					"fix: give each generated command a unique skills/<dir>/SKILL.md path",
				previous, commandID, dir,
			)
			continue
		}
		metadataDirs[dir] = commandID
	}

	emitterDirs := make(map[string]string, len(roleSkillDirs)+len(commandSkillDirs))
	addEmitterDir := func(dir, source string) {
		t.Helper()
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

	for roleID, dir := range roleSkillDirs {
		source := fmt.Sprintf("roleSkillDirs[%q]", roleID)
		addEmitterDir(dir, source)

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

	for commandID, dir := range commandSkillDirs {
		source := fmt.Sprintf("commandSkillDirs[%q]", commandID)
		addEmitterDir(dir, source)

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
	for dir := range SkillBodySpecs {
		bodyDirs[dir] = fmt.Sprintf("SkillBodySpecs[%q]", dir)
	}

	assertSkillDirSetEqual(t, "CommandSpecs", metadataDirs, "harness emitters", emitterDirs)
	assertSkillDirSetEqual(t, "CommandSpecs", metadataDirs, "SkillBodySpecs", bodyDirs)
}

func skillDirFromCommandFile(file string) (string, bool) {
	parts := strings.Split(file, "/")
	if len(parts) != 3 || parts[0] != "skills" || parts[1] == "" || parts[2] != "SKILL.md" {
		return "", false
	}
	return parts[1], true
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
