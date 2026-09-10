package embedded

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtensionMirrorEqual(t *testing.T) {
	embeddedFiles, err := extensionFS.ReadDir("files")
	if err != nil {
		t.Fatal(err)
	}
	// Locate repo root by walking up from this package dir.
	dir, _ := os.Getwd()
	var devDir string
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "extension")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			devDir = candidate
			break
		}
		dir = filepath.Dir(dir)
	}
	if devDir == "" {
		t.Skip("extension dev tree not found")
	}
	for _, e := range embeddedFiles {
		name := e.Name()
		if name == "bridge-config.js" {
			t.Fatalf("generated bridge-config.js must never be embedded")
		}
		emb, err := extensionFS.ReadFile("files/" + name)
		if err != nil {
			t.Fatal(err)
		}
		dev, err := os.ReadFile(filepath.Join(devDir, name))
		if err != nil {
			t.Fatalf("dev mirror missing %s: %v (copy extension/* to pkg/embedded/files/*)", name, err)
		}
		if string(emb) != string(dev) {
			t.Fatalf("mirror drift for %s: copy extension/%s to pkg/embedded/files/%s", name, name, name)
		}
	}
}
