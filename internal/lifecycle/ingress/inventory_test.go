package ingress_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
)

// corpusFixture is one committed authentic capture: its payload bytes (the
// callback value for an OpenCode record), its provenance sidecar, and the
// digest of the payload computed here rather than read from the sidecar, so
// an exemption keyed on it cannot be satisfied by editing the sidecar.
type corpusFixture struct {
	harnessDir string
	name       string
	payload    []byte
	sidecar    map[string]json.RawMessage
	digest     string
}

// committedFixtures derives the corpus from the fixture directories beneath
// this package: every <stem>.json and <stem>.capture.json with its
// <stem>.provenance.json sidecar. The population is read from disk, so a
// fixture added by a later capture campaign is covered the day it lands.
func committedFixtures(t *testing.T) []corpusFixture {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("*", "testdata", "fixtures", "*.json"))
	require.NoError(t, err)
	var fixtures []corpusFixture
	for _, path := range matches {
		if strings.HasSuffix(path, ".provenance.json") {
			continue
		}
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		stem := strings.TrimSuffix(strings.TrimSuffix(path, ".json"), ".capture")
		payload := raw
		if strings.HasSuffix(path, ".capture.json") {
			var record struct {
				Value json.RawMessage `json:"value"`
			}
			require.NoError(t, json.Unmarshal(raw, &record), "%s is not a capture record", path)
			payload = record.Value
		}
		sidecarBytes, err := os.ReadFile(stem + ".provenance.json")
		require.NoError(t, err, "%s has no provenance sidecar", path)
		var sidecar map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(sidecarBytes, &sidecar))
		sum := sha256.Sum256(payload)
		fixtures = append(fixtures, corpusFixture{
			harnessDir: strings.SplitN(path, string(filepath.Separator), 2)[0],
			name:       path,
			payload:    payload,
			sidecar:    sidecar,
			digest:     hex.EncodeToString(sum[:]),
		})
	}
	require.NotEmpty(t, fixtures, "no committed fixture was found beneath this package, so nothing would be checked")
	perHarness := map[string]int{}
	for _, fixture := range fixtures {
		perHarness[fixture.harnessDir]++
	}
	for _, dir := range []string{"claude", "codex", "opencode"} {
		require.Positive(t, perHarness[dir], "the %s fixture directory contributed no fixture; the corpus walk is broken or the directory moved", dir)
	}
	return fixtures
}

// redactionRules reads the rules a sidecar declares through
// acceptance.ParseRedaction, the reader that owns the redaction grammar, so
// the corpus assertion accepts and refuses exactly the values the capture
// provenance accepts and refuses. Only the JSON shape is checked here: the
// member "redaction" is a STRING, as the provenance sidecar declares it, and
// any other JSON shape is refused before the value reaches the parser.
func redactionRules(t *testing.T, fixture corpusFixture) []acceptance.RedactionRule {
	t.Helper()
	raw, present := fixture.sidecar["redaction"]
	require.True(t, present, "%s declares no redaction member; every provenance sidecar states the rules applied, or none", fixture.name)
	var encoded string
	require.NoError(t, json.Unmarshal(raw, &encoded), "%s declares a redaction that is not a JSON string; the provenance shape is one string encoding an ordered, comma-joined rule list", fixture.name)
	rules, err := acceptance.ParseRedaction(encoded)
	require.NoError(t, err, "%s declares a redaction the capture provenance refuses", fixture.name)
	return rules
}

func TestClassifyEveryClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value string
		want  ingress.ValueClass
	}{
		{"5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68", ingress.ClassIdentifier},
		{"PreToolUse", ingress.ClassIdentifier},
		{"2026-08-05T01:40:04Z", ingress.ClassIdentifier},
		{"https://docs.claude.com/en/docs/claude-code/hooks", ingress.ClassIdentifier},
		{"", ingress.ClassIdentifier},
		{"/home/user/project", ingress.ClassPath},
		{"~/.claude/settings.json", ingress.ClassPath},
		{`C:\Users\someone`, ingress.ClassPath},
		{"-home-user-codebases-project", ingress.ClassPath},
		{"Enter the capture code", ingress.ClassFreeText},
		{"line one\nline two", ingress.ClassFreeText},
		{"tab\tseparated", ingress.ClassFreeText},
		{strings.Repeat("a", ingress.FreeTextLengthLimit+1), ingress.ClassFreeText},
		{strings.Repeat("a", ingress.FreeTextLengthLimit), ingress.ClassIdentifier},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, ingress.Classify(tc.value), "%q", tc.value)
	}
	for _, class := range []ingress.ValueClass{ingress.ClassIdentifier, ingress.ClassPath, ingress.ClassFreeText, ingress.ClassNumber, ingress.ClassBool, ingress.ClassNull} {
		assert.True(t, class.IsValid())
		assert.NotEmpty(t, class.String())
	}
	assert.False(t, ingress.ClassInvalid.IsValid())
}

func TestInventoryWalksEveryLeafWithItsPathAndClass(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"session_id":"s1","cwd":"/home/user/p","tool_input":{"command":"ls -la","args":["a","b c"],"count":2,"ok":true,"none":null},"empty":{},"list":[]}`)
	fields, err := ingress.Inventory(payload)
	require.NoError(t, err)
	got := map[string]string{}
	for _, field := range fields {
		got[field.Path] = field.Class.String()
	}
	assert.Equal(t, map[string]string{
		".session_id":         "identifier",
		".cwd":                "path",
		".tool_input.command": "free-text",
		".tool_input.args[0]": "identifier",
		".tool_input.args[1]": "free-text",
		".tool_input.count":   "number",
		".tool_input.ok":      "bool",
		".tool_input.none":    "null",
	}, got)

	_, err = ingress.Inventory([]byte(`[]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not one well-formed JSON object")
}

// jsonShape renders the structure of a document with every scalar replaced by
// its type, so two documents can be compared on keys, nesting, types and
// nulls alone.
func jsonShape(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, member := range v {
			out[key] = jsonShape(member)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for index, member := range v {
			out[index] = jsonShape(member)
		}
		return out
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func TestSubstituteFreeTextKeepsShapeKeysTypesNullsAndLength(t *testing.T) {
	t.Parallel()
	withFreeText := 0
	for _, fixture := range committedFixtures(t) {
		fixture := fixture
		fields, err := ingress.Inventory(fixture.payload)
		require.NoError(t, err)
		var freeText []string
		for _, field := range fields {
			if field.Class == ingress.ClassFreeText {
				freeText = append(freeText, field.Path)
			}
		}
		if len(freeText) == 0 {
			continue
		}
		withFreeText++
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			out, paths, err := ingress.SubstituteFreeText(fixture.payload)
			require.NoError(t, err)
			assert.Equal(t, freeText, paths, "the paths substituted must be exactly the free-text fields, in document order")
			require.Len(t, out, len(fixture.payload), "the substituted document must keep the exact byte length")

			var before, after any
			require.NoError(t, json.Unmarshal(fixture.payload, &before))
			require.NoError(t, json.Unmarshal(out, &after), "the substituted document must still be valid JSON")
			assert.Equal(t, jsonShape(before), jsonShape(after), "keys, nesting, types and nulls must be unchanged")

			afterFields, err := ingress.Inventory(out)
			require.NoError(t, err)
			byPath := map[string]ingress.Field{}
			for _, field := range afterFields {
				byPath[field.Path] = field
			}
			substituted := map[string]struct{}{}
			for _, path := range paths {
				substituted[path] = struct{}{}
			}
			for _, field := range fields {
				got, found := byPath[field.Path]
				require.True(t, found, "field %s disappeared", field.Path)
				if _, was := substituted[field.Path]; was {
					assert.Equal(t, strings.Repeat("x", len(got.Value)), got.Value, "field %s must be placeholder text", field.Path)
					continue
				}
				assert.Equal(t, field.Value, got.Value, "field %s must be untouched", field.Path)
				assert.Equal(t, field.Class, got.Class, "field %s must keep its class", field.Path)
			}

			// A second pass changes nothing: a placeholder longer than the
			// free-text length limit still reads as free text, and it is
			// replaced by itself.
			again, paths, err := ingress.SubstituteFreeText(out)
			require.NoError(t, err)
			assert.Equal(t, out, again, "a second pass must be a no-op on the bytes")
			for _, path := range paths {
				_, was := substituted[path]
				assert.True(t, was, "a second pass reported %s, which was not a placeholder", path)
			}
		})
	}
	require.Positive(t, withFreeText, "no committed fixture carries free text, so the substitution was not exercised on real bytes")

	// An escaped quote inside free text: the raw literal keeps its length and
	// the document stays valid.
	escaped := []byte(`{"m":"say \"hi\" now","id":"k"}`)
	out, paths, err := ingress.SubstituteFreeText(escaped)
	require.NoError(t, err)
	assert.Equal(t, []string{".m"}, paths)
	assert.Len(t, out, len(escaped))
	var decoded map[string]string
	require.NoError(t, json.Unmarshal(out, &decoded))
	assert.Equal(t, "k", decoded["id"])
	assert.Equal(t, strings.Repeat("x", len(`say \"hi\" now`)), decoded["m"])
}

// inventoryReportDirEnv names the capture directory the report mode reads.
// The report is the worker's own tool during clearance; the assertion over
// the committed corpus below is what the tree enforces.
const inventoryReportDirEnv = "PASTURE_INVENTORY_DIR"

func TestFixtureInventoryReport(t *testing.T) {
	dir, set := os.LookupEnv(inventoryReportDirEnv)
	if !set || dir == "" {
		t.Skipf("report mode: set %s to a capture directory to print every field path with its class and flag every free-text field", inventoryReportDirEnv)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	reported := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".provenance.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		fields, err := ingress.Inventory(raw)
		if err != nil {
			fmt.Printf("%s\n  NOT A FIXTURE: %v\n", name, err)
			continue
		}
		fmt.Printf("%s\n", name)
		for _, field := range fields {
			flag := ""
			if field.Class == ingress.ClassFreeText {
				flag = "  FREE TEXT: substitute with " + ingress.FreeTextRule
			}
			fmt.Printf("  %-60s %-11s%s\n", field.Path, field.Class, flag)
		}
		refusals, err := ingress.RefusedFields(raw)
		require.NoError(t, err)
		for _, refusal := range refusals {
			fmt.Printf("  REFUSED %s: %s (%d bytes); this payload cannot be committed\n", refusal.Class, refusal.Path, refusal.Bytes)
		}
		reasons, err := ingress.Unclearable(raw)
		require.NoError(t, err)
		for _, reason := range reasons {
			fmt.Printf("  UNCLEARABLE: %s\n", reason)
		}
		reported++
	}
	fmt.Printf("%d payloads inventoried in %s\n", reported, dir)
}

// freeTextExemptDigests COUNTS the committed fixtures that CARRY FREE TEXT and
// PREDATE THE REDACTION RULE free-text-v1, keyed by the SHA-256 of their
// payload bytes. That is the whole population it names, and it is not the
// clearance exemption: the clearance procedure keeps its own enumerated list
// of the fixtures that predate that procedure, over a different population
// and for a different question, and the two lists are kept apart on purpose.
// A digest key means no other fixture, and no altered copy of these, can
// claim this exemption. The non-vacuity control is the loop after the corpus
// walk: the list is non-empty, every entry must still resolve to a committed
// fixture that carries free text, and a stale entry is an error. These
// fixtures are deleted at the next pin bump, and each entry goes with its
// fixture.
var freeTextExemptDigests = map[string]string{
	"b3a426a5a273ff4a52c5834dc1846295617c706f2427d38a7e40f6b0f0e98112": "claude elicitation_2_1_222.json",
	"2dd6c5e05902d1a07ca86258ef91adfd7b957118cac5c17d5d888f8b533b5e6e": "claude post_tool_batch_2_1_222.json",
	"b7de90f8fa0afb1a62f947b311cde85799417e794e5a58305e920d0006bf3da9": "claude post_tool_use_2_1_222.json",
	"a0ec3e466598b80607b244524e01b859dfb953c5fba3e50ba2546222f73b58b7": "claude post_tool_use_failure_2_1_222.json",
	"77ea0aa2a208418a2883db0cdb003e6fcf2c62856af515027dbe46270b7812e1": "codex pre_tool_use_0_146_0.json",
	"07b16ca0c5f9c8ea3948ac31e1509dd6d1d26cb93f5aa0c4456f04ce255f0cc1": "opencode session_created_1_18_10.capture.json",
}

func TestEveryCommittedFixtureWithFreeTextListsTheFreeTextRule(t *testing.T) {
	t.Parallel()
	for _, name := range []string{ingress.HomePathRule, ingress.FreeTextRule} {
		require.True(t, acceptance.RedactionRule(name).IsValid(), "this package names the substitution %q, which the capture provenance does not accept as a redaction rule; the two names must agree, or no sidecar can declare the substitution", name)
	}
	seen := map[string]bool{}
	for _, fixture := range committedFixtures(t) {
		fields, err := ingress.Inventory(fixture.payload)
		require.NoError(t, err, fixture.name)
		var freeText []string
		for _, field := range fields {
			if field.Class == ingress.ClassFreeText {
				freeText = append(freeText, field.Path)
			}
		}
		if len(freeText) == 0 {
			continue
		}
		rules := redactionRules(t, fixture)
		if _, exempt := freeTextExemptDigests[fixture.digest]; exempt {
			seen[fixture.digest] = true
			continue
		}
		hasRule := false
		for _, rule := range rules {
			if rule == ingress.FreeTextRule {
				hasRule = true
			}
		}
		assert.True(t, hasRule,
			"fixture %s carries free text in field %s (and %d more) but its provenance lists redaction %v without %s; substitute the free text with the rule before committing, or the user's prompt and tool text reaches the repository verbatim",
			fixture.name, freeText[0], len(freeText)-1, rules, ingress.FreeTextRule)
		home, free := -1, -1
		for index, rule := range rules {
			switch rule {
			case ingress.HomePathRule:
				home = index
			case ingress.FreeTextRule:
				free = index
			}
		}
		if home >= 0 && free >= 0 {
			assert.Less(t, home, free, "fixture %s lists the rules out of order; %s is applied before %s", fixture.name, ingress.HomePathRule, ingress.FreeTextRule)
		}
	}
	require.NotEmpty(t, freeTextExemptDigests, "the free-text exemption list is empty while this test still exists; delete the list and this control together when the last legacy fixture goes")
	for digest, name := range freeTextExemptDigests {
		assert.True(t, seen[digest], "the exemption for %s (%s) names a fixture that no longer exists or no longer carries free text; delete the entry", name, digest)
	}
}

func TestNoCommittedFixtureCarriesARefusedClass(t *testing.T) {
	t.Parallel()
	for _, fixture := range committedFixtures(t) {
		refusals, err := ingress.RefusedFields(fixture.payload)
		require.NoError(t, err, fixture.name)
		for _, refusal := range refusals {
			assert.Fail(t, "refused payload class in the committed corpus",
				"fixture %s field %s is %s (%d bytes); this class is never committed whatever the substitution", fixture.name, refusal.Path, refusal.Class, refusal.Bytes)
		}
	}
}

func TestRefusedFieldsNamesEachRefusedClass(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("y", ingress.MaxToolResponseBytes+1)
	cases := []struct {
		name    string
		payload string
		want    []ingress.Refusal
	}{
		{"tool response over the limit", `{"tool_response":"` + big + `"}`, []ingress.Refusal{{Class: ingress.RefusalToolResponseOverLimit, Path: ".tool_response", Bytes: len(big)}}},
		{"nested response content over the limit", `{"tool_response":{"file":{"content":"` + big + `"}}}`, []ingress.Refusal{{Class: ingress.RefusalToolResponseOverLimit, Path: ".tool_response.file.content", Bytes: len(big)}}},
		{"a prompt over the limit is free text, not a refused response", `{"prompt":"` + big + `"}`, nil},
		{"a response at the limit is kept", `{"tool_response":"` + strings.Repeat("y", ingress.MaxToolResponseBytes) + `"}`, nil},
		{"environment dump as an object", `{"env":{"PATH":"/usr/bin","HOME":"/home/u","SHELL":"/bin/sh"}}`, []ingress.Refusal{{Class: ingress.RefusalEnvironmentDump, Path: ".env", Bytes: 3}}},
		{"two constants are not a dump", `{"env":{"PATH":"/usr/bin","HOME":"/home/u"}}`, nil},
		{"environment dump as lines", `{"output":"PATH=/usr/bin\nHOME=/home/u\nSHELL=/bin/sh\n"}`, []ingress.Refusal{{Class: ingress.RefusalEnvironmentDump, Path: ".output", Bytes: len("PATH=/usr/bin\nHOME=/home/u\nSHELL=/bin/sh\n")}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ingress.RefusedFields([]byte(tc.payload))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestUnclearableNamesWhatSubstitutionCannotClear(t *testing.T) {
	t.Parallel()
	token := "sk-ant-" + strings.Repeat("a", 24)
	// A token inside free text is cleared by substitution: clearable.
	reasons, err := ingress.Unclearable([]byte(`{"prompt":"use ` + token + ` please","session_id":"s"}`))
	require.NoError(t, err)
	assert.Empty(t, reasons)
	// The same token as an identifier value survives substitution: unclearable.
	reasons, err = ingress.Unclearable([]byte(`{"api_key":"` + token + `","session_id":"s"}`))
	require.NoError(t, err)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "field .api_key carries a Anthropic API key")
	assert.Contains(t, reasons[0], "value substitution cannot clear")
	// A refused class is unclearable by construction.
	reasons, err = ingress.Unclearable([]byte(`{"tool_response":"` + strings.Repeat("y", ingress.MaxToolResponseBytes+1) + `"}`))
	require.NoError(t, err)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "field .tool_response is refused as tool-response-over-limit")
	sort.Strings(reasons)
}
