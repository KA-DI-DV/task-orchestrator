package pipeline

import (
	"strings"
	"testing"

	"orchestrator/internal/storage"
)

func TestIsApproved(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"чистий вердикт", "Все добре.\nVERDICT: APPROVE", true},
		{"з markdown", "Все добре.\n**VERDICT: APPROVE**", true},
		{"зауваження", "Є проблеми.\nVERDICT: CHANGES_REQUESTED", false},
		{"слово APPROVE у тексті не рахується", "Я не можу APPROVE це.\nVERDICT: CHANGES_REQUESTED", false},
		{"старий формат", "Супер\nAPPROVE", true},
		{"немає вердикту", "Щось пішло не так", false},
	}
	for _, c := range cases {
		// t.Run створює підтест з назвою — у виводі видно, який саме випадок впав.
		t.Run(c.name, func(t *testing.T) {
			if got := isApproved(c.output); got != c.want {
				t.Errorf("isApproved(%q) = %v, очікував %v", c.output, got, c.want)
			}
		})
	}
}

func TestIsTestPassed(t *testing.T) {
	if !isTestPassed("Тести зелені\nTEST_RESULT: PASSED") {
		t.Error("очікував PASSED")
	}
	if isTestPassed("Впав тест X\nTEST_RESULT: FAILED") {
		t.Error("очікував FAILED")
	}
	if isTestPassed("без маркера") {
		t.Error("без маркера тест не має вважатися пройденим")
	}
}

func TestPromptsListRepos(t *testing.T) {
	task := &storage.Task{
		ID:         "PROJ-1",
		BranchName: "feature/PROJ-1",
		Repos: []storage.TaskRepo{
			{RepoName: "backend", WorktreePath: "/tmp/workspaces/PROJ-1/backend", BaseCommit: "aaa", IsMain: true},
			{RepoName: "migrations", WorktreePath: "/tmp/workspaces/PROJ-1/migrations", BaseCommit: "bbb"},
		},
	}

	dev := developerPrompt(task)
	for _, want := range []string{"/tmp/workspaces/PROJ-1/backend (основний", "/tmp/workspaces/PROJ-1/migrations"} {
		if !strings.Contains(dev, want) {
			t.Errorf("developerPrompt не містить %q", want)
		}
	}

	// Рев'юеру — diff лише змінених репозиторіїв.
	rev := reviewerPrompt(task, task.Repos[1:])
	if !strings.Contains(rev, "git -C /tmp/workspaces/PROJ-1/migrations diff bbb...HEAD") {
		t.Errorf("reviewerPrompt без diff для migrations:\n%s", rev)
	}
	if strings.Contains(rev, "diff aaa...HEAD") {
		t.Error("reviewerPrompt містить diff незміненого репозиторію")
	}
}
