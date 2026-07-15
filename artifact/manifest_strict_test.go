package artifact_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

func TestManifestDecoderRejectsDuplicateAndCaseAliasedTopLevelFields(t *testing.T) {
	t.Parallel()

	validEntries := `[{"path":"file","type":"regular-file","mode":"0644","digest":"` + artifact.DigestBytes(alpha).String() + `"}]`
	tests := []struct {
		name string
		json string
		rule string
	}{
		{
			name: "duplicate schema",
			json: `{"schema":"artifact.manifest.v1","schema":"artifact.manifest.v1","entries":` + validEntries + `}`,
			rule: "unique manifest object field",
		},
		{
			name: "duplicate entries",
			json: `{"schema":"artifact.manifest.v1","entries":` + validEntries + `,"entries":` + validEntries + `}`,
			rule: "unique manifest object field",
		},
		{
			name: "case aliased Schema",
			json: `{"Schema":"artifact.manifest.v1","entries":` + validEntries + `}`,
			rule: "exact manifest object field name",
		},
		{
			name: "case aliased Entries",
			json: `{"schema":"artifact.manifest.v1","Entries":` + validEntries + `}`,
			rule: "exact manifest object field name",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := artifact.ParseManifest([]byte(test.json))
			assertManifestCodecRule(t, err, test.rule)
		})
	}
}

func TestManifestDecoderRejectsDuplicateAndCaseAliasedEntryFields(t *testing.T) {
	t.Parallel()

	digest := artifact.DigestBytes(alpha).String()
	base := []string{
		`"path":"file"`,
		`"type":"regular-file"`,
		`"mode":"0644"`,
		`"digest":"` + digest + `"`,
	}
	tests := []struct {
		name  string
		field string
		value string
		rule  string
	}{
		{name: "duplicate path", field: "path", value: `"file"`, rule: "unique manifest entry field"},
		{name: "duplicate type", field: "type", value: `"regular-file"`, rule: "unique manifest entry field"},
		{name: "duplicate mode", field: "mode", value: `"0644"`, rule: "unique manifest entry field"},
		{name: "duplicate digest", field: "digest", value: `"` + digest + `"`, rule: "unique manifest entry field"},
		{name: "case aliased Path", field: "Path", value: `"file"`, rule: "exact manifest entry field name"},
		{name: "case aliased Type", field: "Type", value: `"regular-file"`, rule: "exact manifest entry field name"},
		{name: "case aliased Mode", field: "Mode", value: `"0644"`, rule: "exact manifest entry field name"},
		{name: "case aliased Digest", field: "Digest", value: `"` + digest + `"`, rule: "exact manifest entry field name"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fields := append([]string(nil), base...)
			fields = append(fields, `"`+test.field+`":`+test.value)
			encoded := `{"schema":"artifact.manifest.v1","entries":[{` + strings.Join(fields, ",") + `}]}`
			_, err := artifact.ParseManifest([]byte(encoded))
			assertManifestCodecRule(t, err, test.rule)
		})
	}
}

func assertManifestCodecRule(t *testing.T, err error, rule string) {
	t.Helper()
	if err == nil {
		t.Fatalf("ParseManifest succeeded, want rule %q failure", rule)
	}
	var validation *artifact.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseManifest error = %T %v, want *artifact.ValidationError", err, err)
	}
	if validation.Stage != "manifest decoding" || validation.Rule != rule {
		t.Fatalf("ParseManifest error stage/rule = %q/%q, want manifest decoding/%q", validation.Stage, validation.Rule, rule)
	}
}
