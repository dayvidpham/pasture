package release

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ─── Conventional Commits ────────────────────────────────────────────────────

// commitSections maps conventional commit prefixes to Keep-a-Changelog sections.
var commitSections = map[string]string{
	"feat":     "Added",
	"fix":      "Fixed",
	"refactor": "Changed",
	"perf":     "Changed",
	"docs":     "Documentation",
}

// defaultSection is used when no conventional commit prefix is recognized.
const defaultSection = "Other"

// conventionalCommitRE matches "type(optional-scope): description".
var conventionalCommitRE = regexp.MustCompile(`^(\w+)(?:\((.+?)\))?:\s*(.+)$`)

// ConventionalCommit is a parsed conventional commit message.
type ConventionalCommit struct {
	// Type is the commit type (feat, fix, refactor, …).
	Type string
	// Scope is the optional scope in parentheses (may be empty).
	Scope string
	// Description is the rest of the subject line.
	Description string
	// Raw is the original unmodified subject line.
	Raw string
}

// ParseConventionalCommit parses a conventional commit subject line.
// Returns an error when the line does not match the "type: description" format.
func ParseConventionalCommit(message string) (*ConventionalCommit, error) {
	m := conventionalCommitRE.FindStringSubmatch(strings.TrimSpace(message))
	if m == nil {
		return nil, fmt.Errorf(
			"validation error: %q is not a conventional commit — "+
				"expected format is \"type(scope): description\" (e.g. \"feat: add login\") — "+
				"the commit will be placed in the 'Other' section of the changelog",
			message,
		)
	}
	return &ConventionalCommit{
		Type:        m[1],
		Scope:       m[2],
		Description: m[3],
		Raw:         message,
	}, nil
}

// section returns the Keep-a-Changelog section for this commit type.
func (c *ConventionalCommit) section() string {
	if s, ok := commitSections[strings.ToLower(c.Type)]; ok {
		return s
	}
	return defaultSection
}

// ─── Changelog generation ────────────────────────────────────────────────────

// sectionOrder controls the display order of changelog sections.
var sectionOrder = []string{"Added", "Fixed", "Changed", "Documentation", "Other"}

// GenerateChangelog generates a Keep-a-Changelog markdown entry from commits.
// Commits that do not parse as conventional commits are placed in "Other".
func GenerateChangelog(commits []ConventionalCommit, version SemVer) string {
	today := time.Now().UTC().Format("2006-01-02")

	// Group commit subjects by section.
	groups := make(map[string][]string)
	for _, c := range commits {
		sec := c.section()
		groups[sec] = append(groups[sec], c.Raw)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## [%s] - %s\n", version.String(), today)

	for _, sec := range sectionOrder {
		entries, ok := groups[sec]
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "\n### %s\n", sec)
		for _, e := range entries {
			fmt.Fprintf(&sb, "- %s\n", e)
		}
	}

	return sb.String()
}
