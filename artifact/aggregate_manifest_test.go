package artifact_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

func TestAggregateManifestRejectsMovingAssetAndRevisionMismatch(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../internal/install/releasecatalog/testdata/pasture-aggregate-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, old, replacement string }{
		{"moving alias", `"asset":"pasture-1.2.0-claude-skills.tgz"`, `"asset":"pasture-stable"`},
		{"moving alias token", `"asset":"pasture-1.2.0-claude-skills.tgz"`, `"asset":"pasture-stable-1.2.0.tgz"`},
		{"revision mismatch", `"pasture_revision":"1111111111111111111111111111111111111111"`, `"pasture_revision":"3333333333333333333333333333333333333333"`},
		{"wrong runtime harness", `"runtime_contract":"claude-code/claude-code@2.1.210"`, `"runtime_contract":"codex/codex@0.144.1"`},
		{"unknown runtime profile", `"runtime_contract":"claude-code/claude-code@2.1.210"`, `"runtime_contract":"claude-code/typo"`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := strings.Replace(string(b), test.old, test.replacement, 1)
			if _, err := artifact.ParseAggregateManifest([]byte(changed)); err == nil {
				t.Fatal("expected strict aggregate rejection")
			}
		})
	}
}

func TestAggregateManifestJSONUnmarshalIsStrict(t *testing.T) {
	t.Parallel()
	var manifest artifact.AggregateManifest
	if err := json.Unmarshal([]byte(`{}`), &manifest); err == nil {
		t.Fatal("expected strict unmarshal rejection")
	}
}

func TestVersionOrdering(t *testing.T) {
	t.Parallel()
	final, _ := artifact.ParseVersion("1.2.0")
	rc2, _ := artifact.ParseVersion("1.2.0-rc.2")
	rc10, _ := artifact.ParseVersion("1.2.0-rc.10")
	if final.Compare(rc10) <= 0 || rc10.Compare(rc2) <= 0 {
		t.Fatal("SemVer precedence is incorrect")
	}
}

func TestStrictManifestJSONMatrix(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../internal/install/releasecatalog/testdata/pasture-aggregate-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		data  []byte
		field string
	}{{"duplicate", []byte(strings.Replace(string(b), `"schema":`, `"schema":"pasture.aggregate-release/v1","schema":`, 1)), "manifest.schema"}, {"unknown", []byte(strings.Replace(string(b), `"schema":`, `"unknown":1,"schema":`, 1)), "manifest.unknown"}, {"case variant", []byte(strings.Replace(string(b), `"schema":`, `"Schema":`, 1)), "manifest.Schema"}, {"nested case variant", []byte(strings.Replace(string(b), `"installer_min":`, `"Installer_Min":`, 1)), "manifest.compatibility.Installer_Min"}, {"trailing", append(append([]byte(nil), b...), []byte(` {}`)...), "JSON"}}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := artifact.ParseAggregateManifest(tc.data)
			if err == nil {
				t.Fatal("expected rejection")
			}
			var validation *artifact.AggregateValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("wrong error %T", err)
			}
			if !strings.Contains(validation.Field, tc.field) {
				t.Fatalf("field=%q want %q", validation.Field, tc.field)
			}
		})
	}
}

func TestLargeNumericPrereleaseOrdering(t *testing.T) {
	t.Parallel()
	large, _ := artifact.ParseVersion("1.0.0-rc.100000000000000000000")
	smaller, _ := artifact.ParseVersion("1.0.0-rc.99999999999999999999")
	if large.Compare(smaller) <= 0 {
		t.Fatal("arbitrary precision numeric prerelease ordering failed")
	}
}

func TestMalformedChecksumSidecars(t *testing.T) {
	t.Parallel()
	manifest, err := os.ReadFile("../internal/install/releasecatalog/testdata/pasture-aggregate-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	valid := artifact.AggregateManifestChecksum(manifest)
	cases := []struct {
		name         string
		sidecar      []byte
		stage, field string
	}{
		{"empty", nil, "manifest checksum verification", artifact.AggregateChecksumAsset},
		{"missing newline", bytes.TrimSuffix(valid, []byte("\n")), "manifest checksum verification", artifact.AggregateChecksumAsset},
		{"extra line", append(valid, []byte("extra\n")...), "manifest checksum verification", artifact.AggregateChecksumAsset},
		{"noncanonical spacing", []byte(strings.Repeat("0", 64) + " pasture-aggregate-manifest.json\n"), "manifest checksum verification", artifact.AggregateChecksumAsset},
		{"wrong filename", []byte(strings.Repeat("0", 64) + "  other.json\n"), "manifest checksum verification", artifact.AggregateChecksumAsset},
		{"nonhex", []byte(strings.Repeat("g", 64) + "  pasture-aggregate-manifest.json\n"), "manifest checksum verification", artifact.AggregateChecksumAsset},
	}
	for _, test := range cases {
		_, err := artifact.VerifyAggregateManifest(manifest, test.sidecar)
		var validation *artifact.AggregateValidationError
		if !errors.As(err, &validation) || validation.Stage != test.stage || validation.Field != test.field {
			t.Fatalf("%s: validation=%v err=%v", test.name, validation, err)
		}
	}
}

func TestStrictSemanticFaultMatrix(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../internal/install/releasecatalog/testdata/pasture-aggregate-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	componentLine := `{"id":"claude-code/skills","harness":"claude-code","extension":"skills","asset":"pasture-1.2.0-claude-skills.tgz","digest":"sha256:71f23de8f2deec7f803d7279181636edb9492b97571a78f402d94c00f93429ad","bundle_id":"artifact.bundle.v1:sha256:1111111111111111111111111111111111111111111111111111111111111111","runtime_contract":"claude-code/claude-code@2.1.210","pasture_revision":"1111111111111111111111111111111111111111","aura_revision":"2222222222222222222222222222222222222222"},
`
	cases := []struct {
		name  string
		data  string
		field string
	}{{"channel", strings.Replace(string(b), `"channel":"final"`, `"channel":"beta"`, 1), "channel"}, {"compatibility", strings.Replace(string(b), `"installer_min":"1.0.0"`, `"installer_min":"9.0.0"`, 1), "compatibility"}, {"component count", strings.Replace(string(b), componentLine, "", 1), "components"}, {"duplicate asset", strings.Replace(string(b), `"asset":"pasture-1.2.0-opencode-skills.tgz"`, `"asset":"pasture-1.2.0-claude-skills.tgz"`, 1), "components[3].asset"}, {"mixed case duplicate", strings.Replace(string(b), `"version":"1.2.0"`, `"version":"1.2.0","Version":"1.2.0"`, 1), "manifest.Version"}}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := artifact.ParseAggregateManifest([]byte(tc.data))
			if err == nil {
				t.Fatal("expected rejection")
			}
			var validation *artifact.AggregateValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("%T", err)
			}
			if validation.Stage != "manifest decoding" || !strings.Contains(validation.Field, tc.field) {
				t.Fatalf("stage=%q field=%q", validation.Stage, validation.Field)
			}
		})
	}
}

func TestChecksumErrorsHaveTypedLocation(t *testing.T) {
	t.Parallel()
	manifest, _ := os.ReadFile("../internal/install/releasecatalog/testdata/pasture-aggregate-manifest.json")
	_, err := artifact.VerifyAggregateManifest(manifest, []byte("bad\n"))
	var validation *artifact.AggregateValidationError
	if !errors.As(err, &validation) || validation.Stage != "manifest checksum verification" || validation.Field != artifact.AggregateChecksumAsset {
		t.Fatalf("error=%v", err)
	}
}
