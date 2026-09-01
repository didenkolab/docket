package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A library of apps is one repository. Six apps in six repositories is six
// things to clone, six histories to follow, and six places for the same fix.
func TestAnAppCanLiveInAFolderOfARepository(t *testing.T) {
	library := t.TempDir()
	for _, name := range []string{"tests", "risks"} {
		at := filepath.Join(library, name)
		if err := os.MkdirAll(at, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(at, FileName),
			[]byte("name: "+name+"\ndescription: "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p, cleanup, err := Fetch(library + "#risks")
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "risks" {
		t.Errorf("read %q", p.Name)
	}
	// The whole string is what the vault records. Without the folder, installing
	// again from the same place would look for a manifest that is not there.
	if !strings.HasSuffix(p.Source, "#risks") {
		t.Errorf("it recorded %q, which does not say which app it was", p.Source)
	}
	// The whole library is not a pack: its root has no manifest.
	if _, cleanup, err := Fetch(library); err == nil {
		cleanup()
		t.Error("a directory of apps was read as one app")
	}
}

// The folder is a folder in the repository, not a way out of it.
func TestAnAppFolderCannotLeadOutside(t *testing.T) {
	if _, cleanup, err := Fetch(t.TempDir() + "#../../etc"); err == nil {
		cleanup()
		t.Fatal("a path out of the repository was accepted")
	} else if !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("refused with %q", err)
	}
}
