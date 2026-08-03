package scan

import (
	"regexp"
	"sort"
)

// patternDefinition binds one closed PatternID to the exact regular
// expression the scanner matches against Goldmark leaf-node text.
type patternDefinition struct {
	id      PatternID
	regexp  *regexp.Regexp
	summary string
}

// patternRegistry is the single, code-owned, closed catalog of native
// harness syntax the scanner looks for. It is deliberately narrow and
// precise — literal call-like prefixes, not every prose mention of a
// construct's name — so a match is always "candidate harness syntax" in the
// pasture#47 sense, never a name merely discussed in prose (see
// TestPatternRegistryRequiresCallSyntaxNotBareMentions in candidate_test.go).
//
// Extending this registry (e.g. adding Beads/git process- and task-effect
// lexical forms once #46/#43 need real production classification of them)
// is the single place to do it; every other package function derives its
// behavior from this table.
var patternRegistry = []patternDefinition{
	{
		id:      PatternTeamCreate,
		regexp:  regexp.MustCompile(`\bTeamCreate\s*\(`),
		summary: "TeamCreate( parallel-team spawn call prefix",
	},
	{
		id:      PatternSendMessage,
		regexp:  regexp.MustCompile(`\bSendMessage\s*\(`),
		summary: "SendMessage( assignment-messaging call prefix",
	},
	{
		id:      PatternSkillInvocation,
		regexp:  regexp.MustCompile(`\bSkill\(/`),
		summary: "Skill(/ skill-invocation call prefix",
	},
	{
		id:      PatternAskUserQuestion,
		regexp:  regexp.MustCompile(`\bAskUserQuestion\s*\(`),
		summary: "AskUserQuestion( user-decision call prefix",
	},
}

// matchPatterns returns every non-overlapping pattern match found in text,
// sorted in left-to-right source order (start, then stop, then PatternID to
// break a tie between two patterns matching the identical range). A single
// pattern's own matches are already non-overlapping and left-to-right
// because regexp.FindAllStringIndex returns non-overlapping leftmost
// matches, but matchPatterns itself collects matches pattern-by-pattern
// (registry order) before this sort — without it, two different patterns
// matching inside one node (e.g. "SendMessage(...)" appearing before
// "TeamCreate(...)" in the same fenced code block) would be reported in
// registry order instead of source order, contradicting the issue's
// "deterministic owner/node/range order" reporting contract.
func matchPatterns(text string) []patternMatch {
	var matches []patternMatch
	for _, def := range patternRegistry {
		for _, loc := range def.regexp.FindAllStringIndex(text, -1) {
			matches = append(matches, patternMatch{id: def.id, start: loc[0], stop: loc[1]})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		if matches[i].stop != matches[j].stop {
			return matches[i].stop < matches[j].stop
		}
		return matches[i].id < matches[j].id
	})
	return matches
}

type patternMatch struct {
	id    PatternID
	start int
	stop  int
}
