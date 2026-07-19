package codegen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type RuntimeOperationID string

const (
	RuntimeInvokeSkill      RuntimeOperationID = "invoke-skill"
	RuntimeSpawnAgent       RuntimeOperationID = "spawn-agent"
	RuntimeMessageAgent     RuntimeOperationID = "message-agent"
	RuntimeFollowUpAgent    RuntimeOperationID = "follow-up-agent"
	RuntimeWaitForAgents    RuntimeOperationID = "wait-for-agents"
	RuntimeRequestUserInput RuntimeOperationID = "request-user-input"
	RuntimeCloseAgent       RuntimeOperationID = "close-agent"
)

type RuntimeOperation struct {
	ID       RuntimeOperationID
	Skill    string
	Role     string
	TaskID   string
	Prompt   string
	Message  string
	Axis     string
	Original string
}

type RuntimeDialect interface {
	Render(RuntimeOperation) (string, error)
	Project(string) (string, error)
}

type runtimeDialect struct {
	name       HarnessName
	render     func(RuntimeOperation) (string, error)
	guardTerms []string
}

var (
	ClaudeRuntimeDialect RuntimeDialect = runtimeDialect{
		name:   HarnessClaudeCode,
		render: renderClaudeRuntime,
	}
	OpenCodeRuntimeDialect RuntimeDialect = runtimeDialect{
		name:   HarnessOpenCode,
		render: renderOpenCodeRuntime,
		guardTerms: []string{
			"Skill(", "AskUserQuestion", "TeamCreate", "TeamDelete", "SendMessage", "TaskOutput", "Task(",
		},
	}
)

var skillInvocationPattern = regexp.MustCompile(`Skill\(\s*(?:skill:\s*)?["']?/?pasture:([a-zA-Z0-9:{}_-]+)["']?\s*\)`)

func (d runtimeDialect) Render(operation RuntimeOperation) (string, error) {
	if d.render == nil {
		return "", fmt.Errorf(
			"runtime dialect %q cannot render operation %q — no renderer is configured; add an exhaustive target mapping",
			d.name, operation.ID,
		)
	}
	return d.render(operation)
}

func (d runtimeDialect) Project(content string) (string, error) {
	if d.name == HarnessClaudeCode {
		return content, nil
	}

	var projectionErr error
	content = skillInvocationPattern.ReplaceAllStringFunc(content, func(match string) string {
		if projectionErr != nil {
			return match
		}
		parts := skillInvocationPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			projectionErr = fmt.Errorf("parse typed skill operation from %q", match)
			return match
		}
		rendered, err := d.Render(RuntimeOperation{ID: RuntimeInvokeSkill, Skill: parts[1], Original: match})
		if err != nil {
			projectionErr = err
			return match
		}
		return rendered
	})
	if projectionErr != nil {
		return "", projectionErr
	}

	operations := []struct {
		canonical string
		op        RuntimeOperation
	}{
		{canonical: "Skill(...)", op: RuntimeOperation{ID: RuntimeInvokeSkill, Skill: "<skill>", Original: "Skill(...)"}},
		{canonical: "AskUserQuestion", op: RuntimeOperation{ID: RuntimeRequestUserInput, Original: "AskUserQuestion"}},
		{canonical: "TeamCreate", op: RuntimeOperation{ID: RuntimeSpawnAgent, Original: "TeamCreate"}},
		{canonical: "TeamDelete", op: RuntimeOperation{ID: RuntimeCloseAgent, Original: "TeamDelete"}},
		{canonical: "SendMessage", op: RuntimeOperation{ID: RuntimeMessageAgent, Original: "SendMessage"}},
		{canonical: "TaskOutput", op: RuntimeOperation{ID: RuntimeWaitForAgents, Original: "TaskOutput"}},
		{canonical: "Task(", op: RuntimeOperation{ID: RuntimeSpawnAgent, Original: "Task("}},
	}
	for _, replacement := range operations {
		if !strings.Contains(content, replacement.canonical) {
			continue
		}
		rendered, err := d.Render(replacement.op)
		if err != nil {
			return "", err
		}
		content = strings.ReplaceAll(content, replacement.canonical, rendered)
	}

	content = projectRuntimeProse(d.name, content)
	if unresolved := unresolvedRuntimeTerms(content, d.guardTerms); len(unresolved) > 0 {
		return "", fmt.Errorf(
			"runtime projection for %q left foreign operational syntax [%s] — mark literal cross-harness documentation explicitly or add a typed RuntimeOperation mapping",
			d.name, strings.Join(unresolved, ", "),
		)
	}
	return content, nil
}

func renderClaudeRuntime(operation RuntimeOperation) (string, error) {
	switch operation.ID {
	case RuntimeInvokeSkill:
		return "Skill(/pasture:" + operation.Skill + ")", nil
	case RuntimeSpawnAgent:
		return "Task(", nil
	case RuntimeMessageAgent:
		return "SendMessage", nil
	case RuntimeFollowUpAgent:
		return "SendMessage", nil
	case RuntimeWaitForAgents:
		return "TaskOutput", nil
	case RuntimeRequestUserInput:
		return "AskUserQuestion", nil
	case RuntimeCloseAgent:
		return "TeamDelete", nil
	default:
		return "", unknownRuntimeOperation(HarnessClaudeCode, operation.ID)
	}
}

func renderOpenCodeRuntime(operation RuntimeOperation) (string, error) {
	switch operation.ID {
	case RuntimeInvokeSkill:
		return `skill("` + runtimeSkillName(operation.Skill) + `")`, nil
	case RuntimeSpawnAgent:
		return "task(", nil
	case RuntimeMessageAgent:
		return "task_agent_message", nil
	case RuntimeFollowUpAgent:
		return "task_agent_follow_up", nil
	case RuntimeWaitForAgents:
		return "wait_for_task_agents", nil
	case RuntimeRequestUserInput:
		return "interactive_user_prompt", nil
	case RuntimeCloseAgent:
		return "close_task_agent", nil
	default:
		return "", unknownRuntimeOperation(HarnessOpenCode, operation.ID)
	}
}

func projectRuntimeProse(harness HarnessName, content string) string {
	switch harness {
	case HarnessOpenCode:
		replacer := strings.NewReplacer(
			"Task tool", "task agent tool",
			"Task prompt", "task agent prompt",
			"Task subagent", "task agent",
			"Agent tool", "task agent tool",
			"subagent_type", "agent_type",
			"run_in_background", "background",
			"Skill tool", "skill tool",
		)
		return replacer.Replace(content)
	default:
		return content
	}
}

func runtimeSkillName(skill string) string {
	skill = strings.TrimPrefix(skill, "/")
	skill = strings.TrimPrefix(skill, "pasture:")
	skill = strings.ReplaceAll(skill, ":", "-")
	return skill
}

func unresolvedRuntimeTerms(content string, terms []string) []string {
	var unresolved []string
	for _, term := range terms {
		if strings.Contains(content, term) {
			unresolved = append(unresolved, term)
		}
	}
	sort.Strings(unresolved)
	return unresolved
}

func unknownRuntimeOperation(harness HarnessName, operation RuntimeOperationID) error {
	return fmt.Errorf(
		"runtime dialect %q has no mapping for operation %q — add the target-native rendering before generating this harness",
		harness, operation,
	)
}
