package artifact_test

import (
	"encoding/json"
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
		{"wrong runtime harness", `"runtime_contract":"claude/v1"`, `"runtime_contract":"codex/v1"`},
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
