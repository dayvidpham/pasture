package artifact_test

import (
	"embed"
	"io/fs"
	"os"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

// embeddedBundleSource is compiled into the test binary rather than read from
// the checkout at runtime.
//
//go:embed testdata/embedded
var embeddedBundleSource embed.FS

func TestEmbeddedBundleWorksOutsideSourceCheckout(t *testing.T) {
	source, err := fs.Sub(embeddedBundleSource, "testdata/embedded")
	if err != nil {
		t.Fatalf("fs.Sub(embed.FS): %v", err)
	}
	manifest := mustManifest(t,
		mustDirectoryEntry(t, "config", 0o755),
		mustFileEntry(t, "config/settings.json", 0o644, []byte("{\"embedded\":true}\n")),
		mustFileEntry(t, "launcher.sh", 0o755, []byte("#!/bin/sh\necho embedded\n")),
	)

	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir(empty temp dir): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	bundle, err := artifact.NewBundle(source, manifest)
	if err != nil {
		t.Fatalf("NewBundle(embed.FS) outside checkout: %v", err)
	}
	if got := string(readBundleFile(t, bundle, "launcher.sh")); got != "#!/bin/sh\necho embedded\n" {
		t.Fatalf("embedded launcher = %q", got)
	}
}
