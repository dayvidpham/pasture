package guard

import "testing"

func TestBoundedReaderGuardBites(t *testing.T) {
	t.Parallel()
	source := []byte("package bad\nimport \"database/sql\"\nfunc read(){ _ = `SELECT * FROM lifecycle_occurrences`; _ = sql.ErrNoRows }\n")
	findings := CheckBoundedReaderSource("internal/lifecycle/bad_test.go", source)
	if len(findings) < 2 {
		t.Fatalf("findings=%v, want import and SQL violations", findings)
	}
}

func TestBoundedReaderGuardAllowsPublicReaderTest(t *testing.T) {
	t.Parallel()
	source := []byte("package good\nimport \"github.com/dayvidpham/pasture/internal/lifecycle/model\"\nvar _ model.LifecycleReader\n")
	if findings := CheckBoundedReaderSource("internal/lifecycle/good_test.go", source); len(findings) != 0 {
		t.Fatalf("findings=%v", findings)
	}
}
