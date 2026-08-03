package acceptance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func schemaFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "schema", name+".yaml"))
	if err != nil {
		t.Fatalf("read schema fixture %q: %v", name, err)
	}
	return data
}

func validCorpus(t *testing.T) Corpus {
	t.Helper()
	corpus, err := DecodeCorpus(schemaFixture(t, "valid"))
	if err != nil {
		t.Fatalf("DecodeCorpus(valid): %v", err)
	}
	return corpus
}

func TestDecodeCorpusStrictSchema(t *testing.T) {
	t.Parallel()
	corpus := validCorpus(t)
	if corpus.Schema != SchemaVersion || len(corpus.Cases) != 2 || len(corpus.Operators) != 1 {
		t.Fatalf("decoded corpus = %#v", corpus)
	}
	if corpus.Cases[0].Setup.Actors[0].Kind.String() != "human" {
		t.Fatalf("actor kind = %s, want human", corpus.Cases[0].Setup.Actors[0].Kind)
	}
}

func TestDecodeCorpusRejectsUnknownDuplicateAndVariantFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		want string
	}{{"unknown-field", "field executableCallback not found"}, {"duplicate-field", "mapping key \"schema\" already defined"}, {"invalid-variant", "exactly one matching"}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeCorpus(schemaFixture(t, test.name))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeCorpus(%s) error = %v, want substring %q", test.name, err, test.want)
			}
		})
	}
}

func TestDecodeCorpusRejectsUnknownEnumAndExecutableYAML(t *testing.T) {
	t.Parallel()
	valid := schemaFixture(t, "valid")
	unknown := strings.Replace(string(valid), "class: must-pass", "class: maybe", 1)
	if _, err := DecodeCorpus([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown case class") {
		t.Fatalf("unknown enum error = %v", err)
	}
	anchored := strings.Replace(string(valid), "id: strict-loader", "id: &corpus-id strict-loader", 1)
	if _, err := DecodeCorpus([]byte(anchored)); err == nil || !strings.Contains(err.Error(), "aliases and anchors") {
		t.Fatalf("anchor error = %v", err)
	}
	explicitStandard := strings.Replace(string(valid), "id: strict-loader", "id: !!str strict-loader", 1)
	if _, err := DecodeCorpus([]byte(explicitStandard)); err == nil || !strings.Contains(err.Error(), "explicit YAML tag") {
		t.Fatalf("explicit standard tag error = %v", err)
	}
	explicitCustom := strings.Replace(string(valid), "id: strict-loader", "id: !corpus strict-loader", 1)
	if _, err := DecodeCorpus([]byte(explicitCustom)); err == nil || !strings.Contains(err.Error(), "explicit YAML tag") {
		t.Fatalf("explicit custom tag error = %v", err)
	}
	if err := rejectExecutableYAML(&yaml.Node{Kind: yaml.AliasNode}); err == nil || !strings.Contains(err.Error(), "aliases") {
		t.Fatalf("alias node error = %v", err)
	}
}

func TestDecodeCorpusRejectsMultipleDocumentsTraversalAndInputBounds(t *testing.T) {
	t.Parallel()
	valid := string(schemaFixture(t, "valid"))
	if _, err := DecodeCorpus([]byte(valid + "\n---\n{}\n")); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("multiple document error = %v", err)
	}
	traversal := strings.Replace(valid, `stdin: {inline: "{}"}`, `stdin: {fixture: ../secret.json}`, 1)
	if _, err := DecodeCorpus([]byte(traversal)); err == nil || !strings.Contains(err.Error(), "escapes the reviewed fixture root") {
		t.Fatalf("traversal error = %v", err)
	}
	if _, err := DecodeCorpus(make([]byte, MaxCorpusBytes+1)); err == nil || !strings.Contains(err.Error(), "exceeding") {
		t.Fatalf("corpus byte bound error = %v", err)
	}
	oversizedPath := filepath.Join(t.TempDir(), "oversized.yaml")
	if err := os.WriteFile(oversizedPath, make([]byte, MaxCorpusBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCorpus(oversizedPath); err == nil || !strings.Contains(err.Error(), "input bound") {
		t.Fatalf("file corpus byte bound error = %v", err)
	}
}

func TestValidateCorpusRejectsMalformedTypedBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Corpus)
		want string
	}{
		{"nil-command", func(c *Corpus) { c.Cases[0].Target.Command = nil }, "exactly one matching"},
		{"both-variants", func(c *Corpus) { c.Cases[0].Target.NativeEvent = &NativeEvent{} }, "exactly one matching"},
		{"bad-data-value", func(c *Corpus) { c.Cases[0].Target.Command.Stdin = DataValue{} }, "exactly one of inline or fixture"},
		{"both-data-variants", func(c *Corpus) { c.Cases[0].Target.Command.Stdin.Fixture = "input.json" }, "exactly one of inline or fixture"},
		{"oversized-data", func(c *Corpus) {
			value := strings.Repeat("x", MaxDataValueBytes+1)
			c.Cases[0].Target.Command.Stdin = DataValue{Inline: &value}
		}, "inline bytes exceed"},
		{"bad-digest", func(c *Corpus) { c.Cases[0].Delta.Graph.ByteDigest = "sha256:EMPTY" }, "canonical sha256 digest"},
		{"nil-delta-list", func(c *Corpus) { c.Cases[0].Delta.Graph.Added = nil }, "explicit added/changed/removed"},
		{"bad-expectation", func(c *Corpus) { c.Cases[0].Expect.StdoutJSON = nil }, "requires exitCode/stdoutJSON/stderr"},
		{"bad-compatible-kind", func(c *Corpus) { c.Operators[0].Compatible = []TargetKind{"other"} }, "invalid or duplicate compatible"},
		{"max-per-case", func(c *Corpus) { c.Operators[0].MaxPerCase = 2 }, "requires maxPerCase exactly 1"},
		{"duplicate-actor", func(c *Corpus) { c.Cases[0].Setup.Actors = append(c.Cases[0].Setup.Actors, c.Cases[0].Setup.Actors[0]) }, "repeats actor id"},
		{"oversized-collection", func(c *Corpus) { c.Cases[0].Setup.Actors = slices.Repeat(c.Cases[0].Setup.Actors, MaxSetupRecords+1) }, "record bound"},
		{"oversized-delta", func(c *Corpus) { c.Cases[0].Delta.Graph.Added = slices.Repeat([]string{"row"}, MaxDeltaEntries+1) }, "entry bound"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corpus := validCorpus(t)
			test.edit(&corpus)
			if err := ValidateCorpus(corpus); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateCorpus error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeCorpusRejectsMissingFieldsAndVacuousClasses(t *testing.T) {
	t.Parallel()
	valid := schemaFixture(t, "valid")
	missing := strings.Replace(string(valid), "    mutations: [remove-actor]\n", "", 1)
	if _, err := DecodeCorpus([]byte(missing)); err == nil || !strings.Contains(err.Error(), "mutations are required") {
		t.Fatalf("missing field error = %v", err)
	}
	vacuous := strings.ReplaceAll(string(valid), "class: must-fail", "class: must-pass")
	if _, err := DecodeCorpus([]byte(vacuous)); err == nil || !strings.Contains(err.Error(), "both must-pass and must-fail") {
		t.Fatalf("non-vacuity error = %v", err)
	}
}

func TestDecodeCorpusRejectsPlaceholderDigestAndAmbiguousDataValue(t *testing.T) {
	t.Parallel()
	valid := string(schemaFixture(t, "valid"))
	placeholder := strings.Replace(valid, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "sha256:EMPTY", 1)
	if _, err := DecodeCorpus([]byte(placeholder)); err == nil || !strings.Contains(err.Error(), "never a placeholder") {
		t.Fatalf("placeholder digest error = %v", err)
	}
	ambiguous := strings.Replace(valid, `stdin: {inline: "{}"}`, `stdin: {inline: "{}", fixture: input.json}`, 1)
	if _, err := DecodeCorpus([]byte(ambiguous)); err == nil || !strings.Contains(err.Error(), "exactly one of inline or fixture") {
		t.Fatalf("ambiguous DataValue error = %v", err)
	}
}
