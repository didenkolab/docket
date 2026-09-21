package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

// initVault scaffolds from a template built here, never from the published one.
//
// It used to leave Options.Template empty, which meant `git clone` over the
// network: slow, broken on a machine without one, and — the way it actually
// failed — a test that goes red because somebody improved the template this
// morning. What is under test is that scaffolding works, not what the scaffold
// says. See package vaulttest, whose doc comment said all of this while this
// test quietly did the opposite.
func initVault(t *testing.T, opts Options) (dir string, written []string) {
	t.Helper()
	if opts.Template == "" {
		opts.Template = vaulttest.Template(t)
	}
	dir = filepath.Join(t.TempDir(), "vault")
	written, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return dir, written
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

// Init's contract: everything the template had, less the files that belong to
// the template rather than to a vault, plus the project folder and the boards it
// generates.
//
// Stated as that rather than as a list of names. The list used to be the
// published template's contents, which meant this test went red the day somebody
// added a page to a repository it does not own — and it went red for a change
// that was correct.
func TestInitWritesTheWholeVault(t *testing.T) {
	dir, written := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	templateOnly := map[string]bool{}
	for _, name := range TemplateOnly {
		templateOnly[name] = true
	}

	want := map[string]bool{}
	for _, name := range vaulttest.Files() {
		if !templateOnly[name] {
			want[name] = true
		}
	}
	// The project's own folder, and the boards, which are derived from
	// docket.yaml rather than copied. Named rather than taken from Generated:
	// that needs a configuration, and this test is about which files appear.
	// The sprint board is not among them — a new vault has no sprint pages.
	want["ACME/.gitkeep"] = true
	want[BoardFile] = true
	want[BacklogFile] = true
	want[MineFile] = true
	// The graph settings, which are derived the same way: the colours are this
	// vault's own words for its containers and its statuses.
	want[GraphFile] = true
	// The template's placeholder folder is renamed, not carried over.
	delete(want, "PROJ/.gitkeep")

	got := map[string]bool{}
	for _, p := range written {
		got[p] = true
	}

	for p := range want {
		if !got[p] {
			t.Errorf("Init did not report %s\ngot: %v", p, written)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s is missing on disk: %v", p, err)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("Init wrote %s, which is not part of a new vault", p)
		}
	}

	// The template's own files stay behind. TEMPLATE.md explains the
	// placeholder to whoever edits the template, and means nothing in a vault.
	for _, name := range TemplateOnly {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s was carried into the vault", name)
		}
	}
	// And so does the template's history: a new vault is not a fork.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("the template's .git was carried into the vault")
	}
}

// The licence is the template's, not the board's: a team decides its own
// terms, and init must not decide for it.
func TestInitLeavesTheTemplatesLicenceBehind(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})
	if _, err := os.Stat(filepath.Join(dir, "LICENSE")); err == nil {
		t.Error("the template's LICENSE was carried into the vault")
	}
}

func TestInitStampsKeyAndName(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	config := read(t, dir, "docket.yaml")
	if !strings.Contains(config, "key: ACME") {
		t.Errorf("docket.yaml has no project key:\n%s", config)
	}
	if !strings.Contains(config, "name: Acme Platform") {
		t.Errorf("docket.yaml has no name:\n%s", config)
	}
	if agents := read(t, dir, "AGENTS.md"); !strings.Contains(agents, "ACME-12") {
		t.Error("AGENTS.md does not use the project key in its examples")
	}
	if readme := read(t, dir, "README.md"); !strings.Contains(readme, "Acme Platform") {
		t.Error("README.md does not use the project name")
	}
}

func TestInitLeavesNoUnrenderedPlaceholders(t *testing.T) {
	dir, written := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	for _, rel := range written {
		if strings.Contains(read(t, dir, rel), "{{") {
			t.Errorf("%s still contains a template placeholder", rel)
		}
	}
}

func TestNameDefaultsToKey(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME"})
	if config := read(t, dir, "docket.yaml"); !strings.Contains(config, "name: ACME") {
		t.Errorf("name did not default to the key:\n%s", config)
	}
}

func TestInitCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep", "nested", "vault")
	if _, err := Init(dir, Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatalf("Init into a missing directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docket.yaml")); err != nil {
		t.Errorf("vault not created: %v", err)
	}
}

func TestInitRefusesANonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(dir, Options{Key: "ACME", Template: vaulttest.Template(t)}); err == nil {
		t.Fatal("Init overwrote a non-empty directory")
	} else if !strings.Contains(err.Error(), "notes.md") {
		t.Errorf("the error does not name what was in the way: %v", err)
	}

	if got := read(t, dir, "notes.md"); got != "mine" {
		t.Errorf("the existing file was modified: %q", got)
	}
}

func TestInitRunsInsideAFreshClone(t *testing.T) {
	// `git clone` of an empty repository leaves a .git directory and nothing
	// else. That is the common case and must not be treated as occupied.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(dir, Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatalf("Init refused a directory holding only .git: %v", err)
	}
}

func TestInitRefusesToRunTwice(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME"})
	if _, err := Init(dir, Options{Key: "ACME", Template: vaulttest.Template(t)}); err == nil {
		t.Error("Init ran twice over the same directory")
	}
}

func TestKeysThatAreRejected(t *testing.T) {
	for _, key := range []string{"", "  ", "a", "AC ME", "acme", "1ACME", "ACME-1", "TOOLONGAKEY", "DOCS"} {
		dir := filepath.Join(t.TempDir(), "vault")
		if _, err := Init(dir, Options{Key: key, Template: vaulttest.Template(t)}); err == nil {
			t.Errorf("key %q was accepted", key)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("key %q was rejected but the directory was created anyway", key)
		}
	}
}

func TestKeysThatAreAccepted(t *testing.T) {
	for _, key := range []string{"AC", "ACME", "A1", "PROJECT123"} {
		dir := filepath.Join(t.TempDir(), "vault")
		if _, err := Init(dir, Options{Key: key, Template: vaulttest.Template(t)}); err != nil {
			t.Errorf("key %q was rejected: %v", key, err)
		}
	}
}

func TestKeyAndNameAreTrimmed(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "  ACME  ", Name: "  Acme Platform  "})
	config := read(t, dir, "docket.yaml")
	if !strings.Contains(config, "key: ACME\n") {
		t.Errorf("the key was not trimmed:\n%s", config)
	}
	if !strings.Contains(config, "name: Acme Platform\n") {
		t.Errorf("the name was not trimmed:\n%s", config)
	}
}

// PROJ-12 becomes ACME-12 and PROJ-NUMBER keeps its word.
//
// The substitution is on word boundaries for exactly this: a template that
// explains its own placeholder ("a key is PROJ-NUMBER") would otherwise come
// out of init saying "a key is ACME-NUMBER", which is a sentence about one
// project pretending to be a sentence about the format.
func TestInitSubstitutesTheKeyOnWordBoundaries(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	agents := read(t, dir, "AGENTS.md")
	if !strings.Contains(agents, "ACME-12") {
		t.Errorf("PROJ-12 was not substituted:\n%s", agents)
	}
	if !strings.Contains(agents, "ACME/") {
		t.Errorf("the project folder was not substituted:\n%s", agents)
	}
	if strings.Contains(agents, "PROJ") {
		t.Errorf("a placeholder survived:\n%s", agents)
	}
	if !strings.Contains(agents, "NUMBER") {
		t.Errorf("PROJ-NUMBER lost the word NUMBER, so the sentence about the "+
			"format became a sentence about one project:\n%s", agents)
	}
}
