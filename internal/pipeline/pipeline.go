// Package pipeline — "мозок" оркестратора: скінченний автомат (FSM),
// який проводить задачу через етапи Developer → Reviewer → QA.
//
// Схема переходів:
//
//	CREATED ──► IN_DEV ──► IN_REVIEW ──► APPROVE? ──так──► IN_TEST ──► COMPLETED
//	               ▲            │                              │
//	               │            ні: review_attempts++          └──► FAILED
//	               │            │
//	               └── < max ───┤
//	                            └── >= max ──► IN_TEST (форсовано, з попередженням)
//
// Кожен етап — окрема функція, яка змінює task.Status. Після кожного етапу
// стан зберігається в PostgreSQL. Тому якщо контейнер впаде, задачу можна
// продовжити з того самого місця командою `orchestrator resume PROJ-123`.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"orchestrator/internal/agent"
	"orchestrator/internal/config"
	"orchestrator/internal/jira"
	"orchestrator/internal/storage"
	"orchestrator/internal/workspace"
)

// Назви етапів для таблиці task_logs.
const (
	stepDevelopment = "DEVELOPMENT"
	stepReview      = "REVIEW"
	stepTesting     = "TESTING"
	stepError       = "ERROR"
)

// Файл, у який рев'юер пише зауваження.
const reviewFeedbackFile = "review_feedback.md"

// Pipeline містить усе, що потрібно для виконання задачі.
// Залежності передаються ззовні (у main.go) — так код легко тестувати
// і завжди видно, від чого він залежить.
type Pipeline struct {
	Store           *storage.Store
	Jira            jira.Client
	Workspace       *workspace.Manager
	Runner          *agent.Runner
	Roles           map[string]config.RoleConfig
	MaxReviewCycles int
	CleanupWorktree bool
	MainRepo        string   // основний репозиторій за замовчуванням
	ExcludeRepos    []string // репозиторії, для яких не створюється worktree

	// running — які задачі зараз виконуються в цьому процесі.
	// sync.Map — потокобезпечна мапа (кілька goroutine можуть писати одночасно).
	running sync.Map
}

// Start створює нову задачу в базі: парсить посилання, читає Jira,
// визначає репозиторії. Саму роботу агентів запускає Run.
//
// repoName — основний репозиторій (у ньому запускається claude). Якщо не
// передано — шукаємо мітку "repo:<name>" у Jira, інакше беремо main_repo з конфігу.
// Worktree створюються для всіх репозиторіїв, крім exclude_repos.
func (p *Pipeline) Start(ctx context.Context, jiraInput, repoName string) (*storage.Task, error) {
	key, err := jira.ParseIssueKey(jiraInput)
	if err != nil {
		return nil, err
	}

	existing, err := p.Store.GetTask(ctx, key)
	switch {
	case err == nil:
		return nil, fmt.Errorf("задача %s вже існує (статус %s). Щоб продовжити: orchestrator resume %s",
			key, existing.Status, key)
	case !errors.Is(err, storage.ErrNotFound):
		return nil, err
	}

	slog.Info("читаю задачу з Jira", "key", key)
	issue, err := p.Jira.GetIssue(ctx, key)
	if err != nil {
		return nil, err
	}

	if repoName == "" {
		repoName = jira.RepoFromLabels(issue.Labels)
	}
	if repoName == "" {
		repoName = p.MainRepo
	}
	if repoName == "" {
		return nil, fmt.Errorf("не вказано репозиторій: задай orchestrator.main_repo, передай --repo <назва> або додай у Jira мітку repo:<назва>")
	}

	repoNames, err := p.Workspace.DiscoverRepos(p.ExcludeRepos)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(repoNames, repoName) {
		return nil, fmt.Errorf("основний репозиторій %q не знайдено серед %v (або він у exclude_repos)", repoName, repoNames)
	}

	// Основний репозиторій — першим, решта — у тому порядку, як знайшли.
	repos := []storage.TaskRepo{{
		RepoName:     repoName,
		WorktreePath: p.Workspace.WorktreePath(repoName, key),
		IsMain:       true,
	}}
	for _, name := range repoNames {
		if name != repoName {
			repos = append(repos, storage.TaskRepo{
				RepoName:     name,
				WorktreePath: p.Workspace.WorktreePath(name, key),
			})
		}
	}
	for _, r := range repos {
		if err := p.Workspace.CheckRepo(ctx, r.RepoName); err != nil {
			return nil, err
		}
	}

	task := &storage.Task{
		ID:                key,
		JiraURL:           jiraInput,
		RepoName:          repoName,
		Status:            storage.StatusCreated,
		BranchName:        workspace.BranchName(key),
		WorktreePath:      repos[0].WorktreePath,
		MaxReviewAttempts: p.MaxReviewCycles,
		Title:             issue.Summary,
		Description:       issue.Description,
		Repos:             repos,
	}
	if err := p.Store.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	slog.Info("задачу створено", "key", key, "main_repo", repoName, "repos", len(repos), "title", issue.Summary)
	return task, nil
}

// Resume готує задачу до продовження. Якщо вона впала (FAILED) —
// починаємо знову з розробки, передавши Developer'у причину падіння.
func (p *Pipeline) Resume(ctx context.Context, key string) (*storage.Task, error) {
	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case storage.StatusCompleted:
		return nil, fmt.Errorf("задача %s вже виконана", key)
	case storage.StatusFailed:
		// Дописуємо причину падіння до фідбеку (там може вже бути звіт QA).
		if task.LastError != "" {
			task.LastFeedback = strings.TrimSpace(task.LastFeedback +
				"\n\nПопередній запуск завершився помилкою:\n" + task.LastError)
		}
		task.Status = storage.StatusCreated // заново створимо worktree (на тій самій гілці)
		task.ReviewAttempts = 0
		task.LastError = ""
		if err := p.Store.SaveTask(ctx, task); err != nil {
			return nil, err
		}
	}
	return task, nil
}

// Run виконує задачу від поточного статусу до фінального (COMPLETED/FAILED).
// Це і є головний цикл скінченного автомата.
func (p *Pipeline) Run(ctx context.Context, key string) error {
	// Захист від подвійного запуску однієї задачі в одному процесі.
	if _, alreadyRunning := p.running.LoadOrStore(key, true); alreadyRunning {
		return fmt.Errorf("задача %s вже виконується", key)
	}
	defer p.running.Delete(key)

	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return err
	}

	for !task.Status.IsFinal() {
		slog.Info("етап", "key", key, "status", task.Status, "review_attempts", task.ReviewAttempts)

		var stepErr error
		switch task.Status {
		case storage.StatusCreated:
			stepErr = p.prepare(ctx, task)
		case storage.StatusInDev:
			stepErr = p.develop(ctx, task)
		case storage.StatusInReview:
			stepErr = p.review(ctx, task)
		case storage.StatusInTest:
			stepErr = p.test(ctx, task)
		default:
			stepErr = fmt.Errorf("невідомий статус %q", task.Status)
		}

		if stepErr != nil {
			// Якщо нас зупинили (Ctrl+C / зупинка контейнера) — НЕ позначаємо
			// задачу як FAILED: статус лишається, і її можна продовжити через resume.
			if ctx.Err() != nil {
				slog.Warn("виконання перервано, задачу можна продовжити", "key", key, "status", task.Status)
				return ctx.Err()
			}
			return p.fail(ctx, task, stepErr)
		}

		if err := p.Store.SaveTask(ctx, task); err != nil {
			return err
		}
	}

	slog.Info("задачу завершено", "key", key, "status", task.Status)
	return nil
}

// ───────────────────────────── Етапи ─────────────────────────────

// prepare: CREATED → IN_DEV. Створюємо ізольований git worktree
// для кожного репозиторію задачі.
func (p *Pipeline) prepare(ctx context.Context, task *storage.Task) error {
	// range по індексу: r — вказівник на елемент слайсу, тож зміни
	// потраплять у сам task.Repos (а не в його копію).
	for i := range task.Repos {
		r := &task.Repos[i]
		path, baseCommit, newBranch, err := p.Workspace.Create(ctx, r.RepoName, task.ID)
		if err != nil {
			return fmt.Errorf("worktree для %s: %w", r.RepoName, err)
		}
		r.WorktreePath = path
		// При resume на наявній гілці лишаємо початковий base; якщо ж гілку
		// створено заново (стару видалили як порожню) — base новий.
		if newBranch || r.BaseCommit == "" {
			r.BaseCommit = baseCommit
		}
	}

	main := task.MainRepo()
	if main == nil {
		return errors.New("у задачі немає основного репозиторію")
	}
	task.WorktreePath = main.WorktreePath
	task.BaseCommit = main.BaseCommit
	task.Status = storage.StatusInDev
	return nil
}

// develop: IN_DEV → IN_REVIEW. Developer пише код, ми комітимо результат.
func (p *Pipeline) develop(ctx context.Context, task *storage.Task) error {
	if _, err := p.runAgent(ctx, task, "developer", stepDevelopment, developerPrompt(task)); err != nil {
		return err
	}

	msg := fmt.Sprintf("%s: %s", task.ID, task.Title)
	if task.ReviewAttempts > 0 {
		msg += fmt.Sprintf(" (виправлення після рев'ю #%d)", task.ReviewAttempts)
	}
	if err := p.commitAll(ctx, task, msg); err != nil {
		return err
	}

	task.Status = storage.StatusInReview
	return nil
}

// review: IN_REVIEW → IN_TEST (схвалено або ліміт) або назад у IN_DEV.
func (p *Pipeline) review(ctx context.Context, task *storage.Task) error {
	changed, err := p.changedRepos(ctx, task)
	if err != nil {
		return err
	}
	res, err := p.runAgent(ctx, task, "reviewer", stepReview, reviewerPrompt(task, changed))
	if err != nil {
		return err
	}

	// Читаємо review_feedback.md (якщо рев'юер його створив) і одразу
	// видаляємо, щоб файл не потрапив у коміт.
	feedbackPath := filepath.Join(task.WorktreePath, reviewFeedbackFile)
	feedback, _ := os.ReadFile(feedbackPath) // помилку ігноруємо: файлу може й не бути
	_ = os.Remove(feedbackPath)

	if isApproved(res.Text) {
		slog.Info("рев'ю пройдено ✅", "key", task.ID)
		task.LastFeedback = ""
		task.Status = storage.StatusInTest
		return nil
	}

	// Зауваження: беремо з файлу, а якщо файлу немає — з відповіді агента.
	task.LastFeedback = strings.TrimSpace(string(feedback))
	if task.LastFeedback == "" {
		task.LastFeedback = res.Text
	}
	task.ReviewAttempts++

	// Anti-Loop Guard: не даємо Developer і Reviewer ганяти задачу безкінечно.
	if task.ReviewAttempts >= task.MaxReviewAttempts {
		slog.Warn("досягнуто ліміт рев'ю — форсовано переходимо до тестування",
			"key", task.ID, "attempts", task.ReviewAttempts)
		task.Status = storage.StatusInTest
		return nil
	}

	slog.Info("рев'юер повернув задачу на доопрацювання", "key", task.ID, "attempt", task.ReviewAttempts)
	task.Status = storage.StatusInDev
	return nil
}

// test: IN_TEST → COMPLETED або FAILED.
func (p *Pipeline) test(ctx context.Context, task *storage.Task) error {
	changed, err := p.changedRepos(ctx, task)
	if err != nil {
		return err
	}
	res, err := p.runAgent(ctx, task, "tester", stepTesting, testerPrompt(task, changed))
	if err != nil {
		return err
	}

	// Тестувальник міг дописати тести — зберігаємо їх у гілці.
	if err := p.commitAll(ctx, task, task.ID+": тести від QA-агента"); err != nil {
		return err
	}

	if !isTestPassed(res.Text) {
		// Звіт тестувальника стане фідбеком для Developer'а при resume.
		task.LastFeedback = "Звіт QA:\n" + res.Text
		return errors.New("тестування не пройдено, подробиці — у логах етапу TESTING")
	}

	// Список змінених репозиторіїв беремо ще раз: QA міг дописати тести.
	if changed, err = p.changedRepos(ctx, task); err != nil {
		return err
	}
	p.notifyJira(ctx, task, changed, res.Text)
	p.cleanup(ctx, task)
	task.Status = storage.StatusCompleted
	return nil
}

// ───────────────────────────── Допоміжне ─────────────────────────────

// runAgent запускає claude з налаштуваннями ролі та пише лог у БД.
func (p *Pipeline) runAgent(ctx context.Context, task *storage.Task, role, step, prompt string) (*agent.Result, error) {
	roleCfg := p.Roles[role]

	// claude запускається в основному репозиторії, а worktree інших
	// репозиторіїв задачі відкриваємо йому через --add-dir.
	// slices.Clone — копія, щоб не дописати прапорці в спільний конфіг ролі.
	flags := slices.Clone(roleCfg.CLIFlags)
	for _, r := range task.Repos {
		if !r.IsMain {
			flags = append(flags, "--add-dir", r.WorktreePath)
		}
	}

	res, err := p.Runner.Run(ctx, agent.Request{
		Role:              role,
		WorkDir:           task.WorktreePath,
		Prompt:            prompt,
		SystemInstruction: roleCfg.SystemInstruction,
		Model:             roleCfg.Model,
		ExtraFlags:        flags,
	})

	// Лог зберігаємо завжди — і при успіху, і при помилці.
	if res != nil {
		logText := fmt.Sprintf("=== PROMPT ===\n%s\n\n=== RESULT ===\n%s\n\n=== RAW OUTPUT ===\n%s",
			prompt, res.Text, res.RawLog)
		if logErr := p.Store.AddLog(ctx, task.ID, step, role, logText); logErr != nil {
			slog.Error("не вдалося записати лог", "error", logErr)
		}
	}
	return res, err
}

// fail переводить задачу у FAILED, записує помилку та прибирає worktree.
func (p *Pipeline) fail(ctx context.Context, task *storage.Task, cause error) error {
	slog.Error("задача завершилась помилкою", "key", task.ID, "status", task.Status, "error", cause)

	// Зберігаємо незакомічену роботу в гілках, щоб нічого не загубилося.
	for _, r := range task.Repos {
		_, _ = p.Workspace.CommitAll(ctx, r.WorktreePath, task.ID+": WIP (задача завершилась помилкою)")
	}
	_ = p.Store.AddLog(ctx, task.ID, stepError, "orchestrator", cause.Error())
	p.cleanup(ctx, task)

	task.Status = storage.StatusFailed
	task.LastError = cause.Error()
	if err := p.Store.SaveTask(ctx, task); err != nil {
		return err
	}
	return cause
}

// cleanup видаляє worktree усіх репозиторіїв задачі. Гілки з комітами
// лишаються, а порожні (репозиторій задача не зачепила) — видаляються.
func (p *Pipeline) cleanup(ctx context.Context, task *storage.Task) {
	if !p.CleanupWorktree {
		return
	}
	for _, r := range task.Repos {
		if err := p.Workspace.Remove(ctx, r.RepoName, r.WorktreePath); err != nil {
			slog.Warn("не вдалося видалити worktree", "path", r.WorktreePath, "error", err)
			continue
		}
		if err := p.Workspace.DeleteBranchIfEmpty(ctx, r.RepoName, task.BranchName, r.BaseCommit); err != nil {
			slog.Warn("не вдалося видалити порожню гілку", "repo", r.RepoName, "error", err)
		}
	}
	// Папка задачі (/tmp/workspaces/<KEY>) — видаляється, лише якщо вже порожня.
	_ = os.Remove(filepath.Dir(task.WorktreePath))
}

// commitAll комітить зміни в усіх репозиторіях задачі.
func (p *Pipeline) commitAll(ctx context.Context, task *storage.Task, message string) error {
	for _, r := range task.Repos {
		if _, err := p.Workspace.CommitAll(ctx, r.WorktreePath, message); err != nil {
			return fmt.Errorf("коміт у %s: %w", r.RepoName, err)
		}
	}
	return nil
}

// changedRepos повертає репозиторії, у гілці яких є коміти задачі.
func (p *Pipeline) changedRepos(ctx context.Context, task *storage.Task) ([]storage.TaskRepo, error) {
	var changed []storage.TaskRepo
	for _, r := range task.Repos {
		n, err := p.Workspace.CommitsSince(ctx, r.WorktreePath, r.BaseCommit)
		if err != nil {
			return nil, fmt.Errorf("перевірка змін у %s: %w", r.RepoName, err)
		}
		if n > 0 {
			changed = append(changed, r)
		}
	}
	return changed, nil
}

// notifyJira пише в задачу коментар про результат. Помилка тут не
// ламає задачу — код уже готовий, просто попереджаємо в лозі.
func (p *Pipeline) notifyJira(ctx context.Context, task *storage.Task, changed []storage.TaskRepo, testReport string) {
	names := make([]string, 0, len(changed))
	for _, r := range changed {
		names = append(names, r.RepoName)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🤖 Оркестратор виконав задачу.\n\nГілка: %s\nРепозиторії зі змінами: %s\nІтерацій рев'ю: %d\n",
		task.BranchName, strings.Join(names, ", "), task.ReviewAttempts)
	if task.ReviewAttempts >= task.MaxReviewAttempts {
		b.WriteString("\n⚠️ Рев'ю не було схвалено за ліміт спроб — потрібна ручна перевірка. Останні зауваження:\n")
		b.WriteString(truncate(task.LastFeedback, 3000))
		b.WriteString("\n")
	}
	b.WriteString("\nЗвіт QA:\n")
	b.WriteString(truncate(testReport, 5000))

	if err := p.Jira.AddComment(ctx, task.ID, b.String()); err != nil {
		slog.Warn("не вдалося оновити Jira", "key", task.ID, "error", err)
	}
}

// truncate обрізає текст до max символів.
// Працюємо з []rune (символи), а не з байтами: кирилиця займає 2 байти
// на літеру, і різання по байтах зламало б текст посеред символу.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "\n…(обрізано)"
}
