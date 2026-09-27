package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// newRepo створює git-репозиторій з одним комітом у dir/name.
// t.Helper() — у разі помилки тест покаже рядок, звідки викликали newRepo.
func newRepo(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", path},
		{"-C", path, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestDiscoverRepos(t *testing.T) {
	base := t.TempDir() // тимчасова папка, Go сам видалить її після тесту
	newRepo(t, base, "backend")
	newRepo(t, base, "frontend")
	newRepo(t, base, "migrations")
	_ = os.Mkdir(filepath.Join(base, "not-a-repo"), 0o755)

	m := &Manager{ReposBaseDir: base}
	got, err := m.DiscoverRepos([]string{"frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"backend", "migrations"}; !slices.Equal(got, want) {
		t.Errorf("DiscoverRepos = %v, очікував %v", got, want)
	}
}

// Повний цикл одного репозиторію: worktree → коміт → прибирання.
// Гілка з комітом лишається, гілка без комітів — видаляється.
func TestWorktreeLifecycle(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	ctx := context.Background()
	base := t.TempDir()
	newRepo(t, base, "backend")
	newRepo(t, base, "migrations")
	m := &Manager{ReposBaseDir: base, WorktreesDir: t.TempDir()}
	branch := BranchName("PROJ-1")

	paths := map[string]string{}
	bases := map[string]string{}
	for _, repo := range []string{"backend", "migrations"} {
		path, baseCommit, newBranch, err := m.Create(ctx, repo, "PROJ-1")
		if err != nil {
			t.Fatal(err)
		}
		if !newBranch {
			t.Errorf("%s: очікував нову гілку", repo)
		}
		if want := filepath.Join(m.WorktreesDir, "PROJ-1", repo); path != want {
			t.Errorf("path = %s, очікував %s", path, want)
		}
		paths[repo], bases[repo] = path, baseCommit
	}

	// Зміни лише в backend.
	if err := os.WriteFile(filepath.Join(paths["backend"], "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for repo, path := range paths {
		if _, err := m.CommitAll(ctx, path, "PROJ-1: зміни"); err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
	}
	for repo, want := range map[string]int{"backend": 1, "migrations": 0} {
		if n, err := m.CommitsSince(ctx, paths[repo], bases[repo]); err != nil || n != want {
			t.Errorf("%s: CommitsSince = %d, %v; очікував %d", repo, n, err, want)
		}
	}

	for repo, path := range paths {
		if err := m.Remove(ctx, repo, path); err != nil {
			t.Fatal(err)
		}
		if err := m.DeleteBranchIfEmpty(ctx, repo, branch, bases[repo]); err != nil {
			t.Fatal(err)
		}
	}
	if !branchExists(ctx, m.RepoPath("backend"), branch) {
		t.Error("backend: гілку з комітом не можна видаляти")
	}
	if branchExists(ctx, m.RepoPath("migrations"), branch) {
		t.Error("migrations: порожня гілка мала бути видалена")
	}
}
