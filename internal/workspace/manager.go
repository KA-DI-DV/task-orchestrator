// Package workspace керує git worktree.
//
// Що таке git worktree? Це додаткова "робоча копія" того самого
// репозиторію в іншій папці, на іншій гілці. Історія (.git) спільна,
// а файли — окремі. Тому кілька агентів можуть одночасно працювати
// над різними задачами в одному репозиторії і не заважати одне одному.
//
// Задача може зачіпати кілька репозиторіїв, тому для кожної задачі є своя
// папка, а в ній — worktree усіх її репозиторіїв поруч:
//
//	/workspaces/backend                  ← основна копія (твоя)
//	/workspaces/migrations
//	/tmp/workspaces/PROJ-1/backend       ← гілка feature/PROJ-1
//	/tmp/workspaces/PROJ-1/migrations    ← гілка feature/PROJ-1
//	/tmp/workspaces/PROJ-2/backend       ← гілка feature/PROJ-2
package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type Manager struct {
	ReposBaseDir string // де лежать репозиторії
	WorktreesDir string // куди класти worktree
}

// RepoPath — повний шлях до репозиторію за назвою.
func (m *Manager) RepoPath(repoName string) string {
	return filepath.Join(m.ReposBaseDir, repoName)
}

// WorktreePath — де буде worktree репозиторію для задачі.
func (m *Manager) WorktreePath(repoName, taskKey string) string {
	return filepath.Join(m.WorktreesDir, taskKey, repoName)
}

// DiscoverRepos повертає назви всіх git-репозиторіїв, що лежать прямо
// в ReposBaseDir, крім перелічених у exclude. Порядок — за алфавітом.
func (m *Manager) DiscoverRepos(exclude []string) ([]string, error) {
	entries, err := os.ReadDir(m.ReposBaseDir)
	if err != nil {
		return nil, fmt.Errorf("не вдалося прочитати %s: %w", m.ReposBaseDir, err)
	}
	var repos []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || slices.Contains(exclude, name) {
			continue
		}
		// .git буває папкою (звичайний репозиторій) або файлом (worktree/submodule) —
		// тому просто перевіряємо, що він існує.
		if _, err := os.Stat(filepath.Join(m.ReposBaseDir, name, ".git")); err == nil {
			repos = append(repos, name)
		}
	}
	return repos, nil
}

// BranchName — назва гілки для задачі.
func BranchName(taskKey string) string {
	return "feature/" + taskKey
}

// CheckRepo перевіряє, що репозиторій існує і це справді git.
func (m *Manager) CheckRepo(ctx context.Context, repoName string) error {
	repo := m.RepoPath(repoName)
	if _, err := git(ctx, repo, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("%s не схоже на git-репозиторій: %w", repo, err)
	}
	return nil
}

// Create створює worktree з новою гілкою від поточного HEAD репозиторію.
// Повертає SHA коміту, від якого відгалузилися (потім рев'юер дивиться
// git diff саме від нього), і newBranch = true, якщо гілку щойно створено.
//
// Якщо worktree вже існує (наприклад, задачу перезапустили) — просто
// використовуємо його.
func (m *Manager) Create(ctx context.Context, repoName, taskKey string) (path, baseCommit string, newBranch bool, err error) {
	repo := m.RepoPath(repoName)
	path = m.WorktreePath(repoName, taskKey)
	branch := BranchName(taskKey)

	baseCommit, err = git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return "", "", false, err
	}

	// Worktree вже є — нічого не робимо.
	if _, statErr := os.Stat(path); statErr == nil {
		return path, baseCommit, false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", false, err
	}

	// Прибираємо "мертві" записи про worktree, папки яких уже видалені.
	_, _ = git(ctx, repo, "worktree", "prune")

	newBranch = !branchExists(ctx, repo, branch)
	if newBranch {
		// -b створює нову гілку.
		_, err = git(ctx, repo, "worktree", "add", "-b", branch, path, baseCommit)
	} else {
		// Гілка лишилася з минулого запуску — продовжуємо на ній.
		_, err = git(ctx, repo, "worktree", "add", path, branch)
	}
	if err != nil {
		return "", "", false, err
	}
	return path, baseCommit, newBranch, nil
}

// CommitsSince рахує коміти в dir від base до HEAD.
// 0 означає, що в цьому репозиторії задача нічого не змінила.
func (m *Manager) CommitsSince(ctx context.Context, dir, base string) (int, error) {
	out, err := git(ctx, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscan(out, &n)
	return n, err
}

// CommitAll комітить усі зміни в worktree (якщо вони є).
// Повертає true, якщо коміт було створено.
func (m *Manager) CommitAll(ctx context.Context, worktree, message string) (bool, error) {
	status, err := git(ctx, worktree, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if status == "" {
		return false, nil // змін немає
	}
	if _, err := git(ctx, worktree, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := git(ctx, worktree, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// Remove видаляє worktree. Гілка з комітами лишається в репозиторії —
// її можна переглянути, запушити або створити з неї Pull Request.
func (m *Manager) Remove(ctx context.Context, repoName, worktree string) error {
	repo := m.RepoPath(repoName)
	if _, err := git(ctx, repo, "worktree", "remove", "--force", worktree); err != nil {
		return err
	}
	return nil
}

// Stats — підсумок змін задачі в репозиторії (для історії в БД):
// скільки комітів, останній коміт і git diff --stat від base.
func (m *Manager) Stats(ctx context.Context, dir, base string) (commits int, head, diffStat string, err error) {
	if commits, err = m.CommitsSince(ctx, dir, base); err != nil {
		return 0, "", "", err
	}
	if head, err = git(ctx, dir, "rev-parse", "HEAD"); err != nil {
		return 0, "", "", err
	}
	if commits > 0 {
		// --stat=120 — ширина рядка, --stat-count=60 — не більше 60 файлів у списку.
		if diffStat, err = git(ctx, dir, "diff", "--stat=120", "--stat-count=60", base+"..HEAD"); err != nil {
			return 0, "", "", err
		}
	}
	return commits, head, diffStat, nil
}

// DeleteBranchIfEmpty видаляє гілку, якщо в ній немає комітів після base.
// Так у репозиторіях, які задача не зачепила, не лишається порожніх гілок.
// Викликати лише після Remove: гілку, з якою пов'язаний worktree, git видалити не дасть.
func (m *Manager) DeleteBranchIfEmpty(ctx context.Context, repoName, branch, base string) error {
	repo := m.RepoPath(repoName)
	if base == "" || !branchExists(ctx, repo, branch) {
		return nil
	}
	out, err := git(ctx, repo, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return err
	}
	if out != "0" {
		return nil // є коміти — гілку лишаємо
	}
	_, err = git(ctx, repo, "branch", "-D", branch)
	return err
}

func branchExists(ctx context.Context, repo, branch string) bool {
	_, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// git запускає команду git у папці dir і повертає її вивід.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
