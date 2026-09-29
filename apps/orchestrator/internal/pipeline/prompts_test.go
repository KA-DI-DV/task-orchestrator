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

func TestExtractBrief(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
	}{
		{
			"рев'юер: до вердикту",
			"Довгий технічний текст.\n\n## Підсумок простою мовою\n- Перевірив логіку\n- Можна приймати\n\nVERDICT: APPROVE",
			"- Перевірив логіку\n- Можна приймати",
		},
		{
			"QA: маркер у markdown",
			"### **Підсумок простою мовою:**\n- Тести пройшли\n**TEST_RESULT: PASSED**",
			"- Тести пройшли",
		},
		{
			"розробник: до кінця відповіді",
			"Зроблено.\n\n## Підсумок простою мовою\n- Додав фільтр\n- Тести оновлено\n",
			"- Додав фільтр\n- Тести оновлено",
		},
		{
			"до наступного заголовка",
			"## Підсумок простою мовою\n- Пункт\n## Деталі\nщось",
			"- Пункт",
		},
		{"немає розділу", "Просто відповідь\nVERDICT: APPROVE", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractBrief(c.output); got != c.want {
				t.Errorf("extractBrief() = %q, очікував %q", got, c.want)
			}
		})
	}
}

func TestPromptsAskForBrief(t *testing.T) {
	task := &storage.Task{ID: "PROJ-1", BranchName: "feature/PROJ-1"}
	for name, prompt := range map[string]string{
		"developer": developerPrompt(task),
		"reviewer":  reviewerPrompt(task, nil),
		"tester":    testerPrompt(task, nil),
	} {
		if !strings.Contains(prompt, "## "+briefHeading) {
			t.Errorf("%s: у завданні немає вимоги до розділу %q", name, briefHeading)
		}
		if strings.Contains(prompt, "%!") {
			t.Errorf("%s: зламане форматування fmt:\n%s", name, prompt)
		}
	}
}

func TestArchitectPrompt(t *testing.T) {
	task := &storage.Task{ID: "PROJ-1", Title: "Ліміти ставок", BranchName: "feature/PROJ-1"}

	first := architectPrompt(task)
	for _, want := range []string{"## Флоу запиту", "## Алгоритм", "## Integration-тести (обов'язково)", "НІЧОГО не змінюй"} {
		if !strings.Contains(first, want) {
			t.Errorf("architectPrompt не містить %q", want)
		}
	}
	if strings.Contains(first, "Попередня версія плану") {
		t.Error("перша версія не має згадувати попередній план")
	}

	// Правки людини: архітектор бачить попередній план і кожну правку.
	task.Plan, task.PlanRevision, task.PlanFeedback = "# План: v1", 1, "Додай тест на ліміт 0"
	revised := architectPrompt(task)
	for _, want := range []string{"Попередня версія плану (версія 1)", "# План: v1", "Додай тест на ліміт 0", "Що змінено у цій версії"} {
		if !strings.Contains(revised, want) {
			t.Errorf("architectPrompt з правками не містить %q", want)
		}
	}
}

func TestPlanInOtherPrompts(t *testing.T) {
	task := &storage.Task{ID: "PROJ-1", BranchName: "feature/PROJ-1"}
	// Стара задача без плану — розділу немає.
	if strings.Contains(developerPrompt(task), "Затверджений план") {
		t.Error("без плану developerPrompt не має містити розділ плану")
	}

	task.Plan = "# План: ліміти\n## Integration-тести (обов'язково)\n- TestBetLimit"
	checks := map[string]string{
		"developer": developerPrompt(task),
		"reviewer":  reviewerPrompt(task, nil),
		"tester":    testerPrompt(task, nil),
	}
	for role, prompt := range checks {
		if !strings.Contains(prompt, "TestBetLimit") {
			t.Errorf("%s: промпт не містить затвердженого плану", role)
		}
		if !strings.Contains(prompt, "integration-тести з") {
			t.Errorf("%s: промпт не вимагає integration-тестів з плану", role)
		}
	}
}

func TestExtractPlan(t *testing.T) {
	cases := map[string]string{
		"# План: X\n## Коротко\nтекст":                "# План: X\n## Коротко\nтекст",
		"Ось план:\n\n# План: X\n## Коротко\nтекст\n": "# План: X\n## Коротко\nтекст",
		"  без заголовка  ":                           "без заголовка",
		"":                                            "",
	}
	for in, want := range cases {
		if got := extractPlan(in); got != want {
			t.Errorf("extractPlan(%q) = %q, очікував %q", in, got, want)
		}
	}
}
