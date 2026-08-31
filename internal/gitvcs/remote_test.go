package gitvcs

import (
	"os"
	"path/filepath"
	"testing"
)

// A proposal you are asked to review is somebody else's, which means it is on
// the remote and not in this clone. A branch list that showed only local
// branches showed only your own proposals — the half nobody needed to review.
func TestBranchesIncludeTheOnesOnlyOnTheRemote(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	clone := filepath.Join(root, "clone")

	run(t, root, "init", "-q", "-b", "main", "origin")
	write(t, origin, "a.md", "one")
	run(t, origin, "add", "a.md")
	run(t, origin, "-c", "user.name=A", "-c", "user.email=a@example.com",
		"commit", "-q", "-m", "first", "--", "a.md")
	// A proposal pushed by somebody else, and a Cyrillic name because that is
	// what these vaults are written in.
	run(t, origin, "checkout", "-q", "-b", "предложение/сроки")
	write(t, origin, "a.md", "two")
	run(t, origin, "add", "a.md")
	run(t, origin, "-c", "user.name=A", "-c", "user.email=a@example.com",
		"commit", "-q", "-m", "re-scope the quarter", "--", "a.md")
	run(t, origin, "checkout", "-q", "main")

	run(t, root, "clone", "-q", origin, clone)

	repo, err := Open(clone)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	branches, err := repo.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}

	var found *Branch
	for i, b := range branches {
		if b.Name == "origin/предложение/сроки" {
			found = &branches[i]
		}
		if b.Name == "origin/main" {
			t.Error("origin/main is listed as well as main; the same proposal twice")
		}
		// git shortens refs/remotes/origin/HEAD to "origin". Both spellings are
		// the same non-proposal, and the short one is the one that got through.
		if b.Name == "origin/HEAD" || b.Name == "origin" {
			t.Errorf("%q is listed, and it is not a proposal", b.Name)
		}
	}
	if found == nil {
		t.Fatalf("the remote proposal is not in the list: %+v", branches)
	}
	if !found.Remote {
		t.Error("it is not marked as being on the remote only")
	}
	if found.Subject != "re-scope the quarter" {
		t.Errorf("subject %q", found.Subject)
	}
	if branches[0].Name != "main" || !branches[0].Current {
		t.Errorf("the checked-out branch is not first: %+v", branches[0])
	}
}

// Reviewing a proposal must not need a checkout: somebody is working in the
// tree, and disturbing them to read a branch is not a trade worth making.
func TestFetchBringsARefDownWithoutTouchingTheTree(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	clone := filepath.Join(root, "clone")

	run(t, root, "init", "-q", "-b", "main", "origin")
	write(t, origin, "a.md", "one")
	run(t, origin, "add", "a.md")
	run(t, origin, "-c", "user.name=A", "-c", "user.email=a@example.com",
		"commit", "-q", "-m", "first", "--", "a.md")
	run(t, root, "clone", "-q", origin, clone)

	// Pushed after the clone, so this one is genuinely not here yet.
	run(t, origin, "checkout", "-q", "-b", "later")
	write(t, origin, "a.md", "two")
	run(t, origin, "add", "a.md")
	run(t, origin, "-c", "user.name=A", "-c", "user.email=a@example.com",
		"commit", "-q", "-m", "later still", "--", "a.md")
	run(t, origin, "checkout", "-q", "main")

	repo, err := Open(clone)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := repo.Fetch(Credential{}, "later"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	found := false
	branches, _ := repo.Branches()
	for _, b := range branches {
		if b.Name == "origin/later" {
			found = true
		}
	}
	if !found {
		t.Errorf("the fetched ref is not readable: %+v", branches)
	}
	if repo.Current() != "main" {
		t.Errorf("the working tree moved to %q", repo.Current())
	}
	if body, err := readFile(clone, "a.md"); err != nil || body != "one" {
		t.Errorf("the working tree changed: %q, %v", body, err)
	}
}

func readFile(dir, name string) (string, error) {
	body, err := os.ReadFile(filepath.Join(dir, name))
	return string(body), err
}
