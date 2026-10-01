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

// Незамерджену гілку можна прибрати повністю, замерджену — MergedInto розпізнає.
func TestMergedIntoAndPurge(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	ctx := context.Background()
	base := t.TempDir()
	newRepo(t, base, "backend")
	m := &Manager{ReposBaseDir: base, WorktreesDir: t.TempDir()}
	branch := BranchName("PROJ-1")
	repo := m.RepoPath("backend")

	path, baseCommit, _, err := m.Create(ctx, "backend", "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}

	// Порожня гілка — не «замерджена».
	if into, err := m.MergedInto(ctx, "backend", baseCommit, baseCommit); err != nil || into != "" {
		t.Fatalf("порожня гілка: MergedInto = %q, %v", into, err)
	}

	_ = os.WriteFile(filepath.Join(path, "a.txt"), []byte("a"), 0o644)
	if _, err := m.CommitAll(ctx, path, "feat"); err != nil {
		t.Fatal(err)
	}
	head, _ := git(ctx, repo, "rev-parse", branch)
	if into, err := m.MergedInto(ctx, "backend", baseCommit, head); err != nil || into != "" {
		t.Fatalf("незамерджена гілка: MergedInto = %q, %v", into, err)
	}

	// Мерджимо гілку в main — тепер видаляти не можна.
	if _, err := git(ctx, repo, "merge", "-q", "--no-ff", "-m", "merge", branch); err != nil {
		t.Fatal(err)
	}
	if into, err := m.MergedInto(ctx, "backend", baseCommit, head); err != nil || into != "main" {
		t.Fatalf("замерджена гілка: MergedInto = %q, %v, очікував main", into, err)
	}

	// Purge прибирає worktree і гілку; за збереженим head мердж усе одно видно.
	if err := m.Purge(ctx, "backend", path, branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree %s не видалено", path)
	}
	if branchExists(ctx, repo, branch) {
		t.Errorf("гілку %s не видалено", branch)
	}
	if into, _ := m.MergedInto(ctx, "backend", baseCommit, head); into != "main" {
		t.Errorf("за head: MergedInto = %q, очікував main", into)
	}
}

// Гілку задачі, в якій задача нічого не комітила, створили поза оркестратором
// від свіжого main (git branch feature/X main). Коміти main між base і гілкою —
// не коміти задачі, тож задача не вважається замердженою.
func TestMergedIntoIgnoresBranchRecreatedFromMain(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	ctx := context.Background()
	base := t.TempDir()
	newRepo(t, base, "backend")
	m := &Manager{ReposBaseDir: base, WorktreesDir: t.TempDir()}
	branch := BranchName("PROJ-2")
	repo := m.RepoPath("backend")

	baseCommit, _ := git(ctx, repo, "rev-parse", "HEAD")
	// main пішов уперед (чужі коміти), гілку задачі створили від нього.
	_ = os.WriteFile(filepath.Join(repo, "other.txt"), []byte("x"), 0o644)
	if _, err := git(ctx, repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, repo, "commit", "-q", "-m", "someone else's work"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, repo, "branch", branch, "HEAD"); err != nil {
		t.Fatal(err)
	}

	// Оркестратор записав head == base: задача тут нічого не зробила.
	if into, err := m.MergedInto(ctx, "backend", baseCommit, baseCommit); err != nil || into != "" {
		t.Fatalf("MergedInto = %q, %v, очікував \"\"", into, err)
	}
}

// DiscardChanges прибирає змінені й нові файли, але не чіпає коміти.
func TestDiscardChanges(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	ctx := context.Background()
	base := t.TempDir()
	newRepo(t, base, "backend")
	m := &Manager{ReposBaseDir: base, WorktreesDir: t.TempDir()}
	path, _, _, err := m.Create(ctx, "backend", "PROJ-3")
	if err != nil {
		t.Fatal(err)
	}

	if discarded, err := m.DiscardChanges(ctx, path); err != nil || discarded {
		t.Fatalf("чистий worktree: DiscardChanges = %v, %v", discarded, err)
	}

	_ = os.WriteFile(filepath.Join(path, "new.txt"), []byte("x"), 0o644)
	if discarded, err := m.DiscardChanges(ctx, path); err != nil || !discarded {
		t.Fatalf("DiscardChanges = %v, %v, очікував true", discarded, err)
	}
	if _, err := os.Stat(filepath.Join(path, "new.txt")); !os.IsNotExist(err) {
		t.Error("новий файл не видалено")
	}
}

func TestCopyIgnored(t *testing.T) {
	m := &Manager{ReposBaseDir: t.TempDir()}
	repo := m.RepoPath("backend")
	worktree := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, "config", "play", "server.json"), "from-repo")
	write(filepath.Join(repo, ".env"), "A=1")
	// Уже є у worktree — не перезаписуємо.
	write(filepath.Join(worktree, "config", "session.json"), "agent-edit")
	write(filepath.Join(repo, "config", "session.json"), "from-repo")

	if err := m.CopyIgnored("backend", worktree, []string{"config", ".env", "missing"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"config/play/server.json": "from-repo",
		".env":                    "A=1",
		"config/session.json":     "agent-edit",
	} {
		got, err := os.ReadFile(filepath.Join(worktree, path))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), очікував %q", path, got, err, want)
		}
	}
}
