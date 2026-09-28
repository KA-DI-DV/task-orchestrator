package pipeline

import (
	"fmt"
	"strings"

	"orchestrator/internal/storage"
)

// Тут зібрані тексти завдань (prompts) для кожного агента.
//
// Роль і "характер" агента задаються в config.yaml (system_instruction),
// а тут — конкретне завдання: що за задача, що вже було, і в якому
// форматі відповісти, щоб оркестратор зміг автоматично зрозуміти результат.

// Маркери, які агенти мають написати останнім рядком відповіді.
const (
	verdictPrefix  = "VERDICT:"
	verdictApprove = "APPROVE"
	verdictChanges = "CHANGES_REQUESTED"
	testPrefix     = "TEST_RESULT:"
	testPassed     = "PASSED"
	testFailed     = "FAILED"
)

// Вердикти в історії задачі (task_logs.verdict), крім наведених вище.
const (
	verdictDone        = "DONE"        // Developer завершив ітерацію
	verdictError       = "ERROR"       // агент або оркестратор завершився помилкою
	verdictInterrupted = "INTERRUPTED" // задачу зупинили (Ctrl+C / зупинка контейнера)
)

func taskHeader(t *storage.Task) string {
	return fmt.Sprintf("Задача Jira %s: %s\n\nОпис задачі:\n%s\n", t.ID, t.Title, t.Description)
}

// workspaceSection описує агенту, які репозиторії є і де лежать їхні worktree.
func workspaceSection(t *storage.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Репозиторії\nКожен репозиторій нижче — окремий git worktree на гілці %s:\n", t.BranchName)
	for _, r := range t.Repos {
		if r.IsMain {
			fmt.Fprintf(&b, "- %s — %s (основний, поточна папка)\n", r.RepoName, r.WorktreePath)
		} else {
			fmt.Fprintf(&b, "- %s — %s\n", r.RepoName, r.WorktreePath)
		}
	}
	return b.String()
}

// diffSection — як подивитися зміни в кожному зміненому репозиторії.
func diffSection(changed []storage.TaskRepo) string {
	if len(changed) == 0 {
		return "Змін немає в жодному репозиторії.\n"
	}
	var b strings.Builder
	b.WriteString("Зміни є в таких репозиторіях:\n")
	for _, r := range changed {
		fmt.Fprintf(&b, "- %s: git -C %s diff %s...HEAD\n", r.RepoName, r.WorktreePath, r.BaseCommit)
	}
	return b.String()
}

func developerPrompt(t *storage.Task) string {
	var b strings.Builder
	b.WriteString("Ти — Developer. Реалізуй задачу нижче.\n\n")
	b.WriteString(taskHeader(t))
	b.WriteString(workspaceSection(t))

	switch {
	case t.LastFeedback != "" && t.ReviewAttempts > 0:
		fmt.Fprintf(&b, "\n## Зауваження рев'юера, які треба виправити (ітерація %d з %d)\n%s\n",
			t.ReviewAttempts, t.MaxReviewAttempts, t.LastFeedback)
	case t.LastFeedback != "":
		// Задачу перезапустили після падіння (resume) — показуємо, що пішло не так.
		fmt.Fprintf(&b, "\n## Проблеми попереднього запуску, які треба виправити\n%s\n", t.LastFeedback)
	}

	fmt.Fprintf(&b, `
## Правила
- Усі репозиторії вже на гілці %s. Не перемикай гілки і не роби git push.
- Задача може потребувати змін у кількох репозиторіях — вноси їх у відповідних
  папках зі списку вище. Репозиторіїв поза цим списком не чіпай.
- Дотримуйся правил кожного репозиторію, який змінюєш (CLAUDE.md, .cursorrules, існуючий стиль коду).
- Напиши або онови юніт-тести для своїх змін.
- Оркестратор сам закомітить зміни в усіх репозиторіях після твоєї роботи.
- Наприкінці коротко опиши, що саме зроблено.
`, t.BranchName)
	return b.String()
}

func reviewerPrompt(t *storage.Task, changed []storage.TaskRepo) string {
	return fmt.Sprintf(`Ти — Reviewer. Зроби код-рев'ю змін для задачі нижче.

%s%s
## Як дивитися зміни
%s- Перевір відповідність вимогам задачі, коректність, тести, стиль, безпеку.
- Якщо зміни в кількох репозиторіях — перевір, що вони узгоджені між собою
  (контракти API, міграції, конфіги).
- Якщо задача вимагала змін у репозиторії, якого немає серед змінених, — це зауваження.
- НЕ змінюй код сам.

## Формат відповіді (обов'язково)
- Якщо все добре — останнім рядком напиши рівно: %s %s
- Якщо є зауваження — запиши їх конкретним списком (з назвою репозиторію біля кожного)
  у файл %s у поточній папці, а останнім рядком напиши рівно: %s %s
`, taskHeader(t), workspaceSection(t), diffSection(changed),
		verdictPrefix, verdictApprove,
		reviewFeedbackFile, verdictPrefix, verdictChanges)
}

func testerPrompt(t *storage.Task, changed []storage.TaskRepo) string {
	return fmt.Sprintf(`Ти — QA-інженер. Перевір, що задача нижче реалізована і працює.

%s%s
%s
## Що зробити
1. У кожному зміненому репозиторії знайди і запусти локальні тести (unit/integration).
2. Якщо в проєкті є веб-інтерфейс і тобі доступний Browser MCP (Playwright) —
   запусти застосунок і перевір UI-сценарії з опису задачі в headless-режимі.
3. Можеш дописати відсутні тести, але не змінюй бізнес-логіку.

## Формат відповіді (обов'язково)
Коротко опиши, що перевірено і з яким результатом, а останнім рядком напиши рівно:
%s %s — якщо все працює, або %s %s — якщо є проблеми.
`, taskHeader(t), workspaceSection(t), diffSection(changed), testPrefix, testPassed, testPrefix, testFailed)
}

// isApproved перевіряє, чи рев'юер схвалив зміни.
func isApproved(output string) bool {
	verdict := findMarker(output, verdictPrefix)
	if verdict != "" {
		return verdict == verdictApprove
	}
	// Запасний варіант зі специфікації: агент просто написав "APPROVE" останнім рядком.
	return lastLine(output) == verdictApprove
}

// isTestPassed перевіряє, чи тестувальник підтвердив успіх.
func isTestPassed(output string) bool {
	return findMarker(output, testPrefix) == testPassed
}

// findMarker шукає ОСТАННІЙ рядок, що починається з prefix, і повертає
// те, що після нього. Наприклад, для "VERDICT: APPROVE" і prefix "VERDICT:"
// поверне "APPROVE". Шукаємо з кінця, бо підсумок пишуть наприкінці.
func findMarker(output, prefix string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		// Прибираємо пробіли та markdown-прикраси типу **VERDICT: APPROVE**.
		line := strings.Trim(strings.TrimSpace(lines[i]), "*`_ ")
		if value, found := strings.CutPrefix(line, prefix); found {
			return strings.ToUpper(strings.Trim(value, "*`_ ."))
		}
	}
	return ""
}

func lastLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "*`_ .")
}
