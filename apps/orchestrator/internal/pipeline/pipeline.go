// Package pipeline — "мозок" оркестратора: скінченний автомат (FSM),
// який проводить задачу через етапи Architect → (людина) → Developer → Reviewer → QA.
//
// Схема переходів:
//
//	CREATED ──► IN_PLANNING ──► PLAN_REVIEW ──схвалено──► IN_DEV ...
//	                 ▲               │
//	                 └──── правки ───┘   (чекаємо на людину: агенти не працюють)
//
//	IN_DEV ──► IN_REVIEW ──► APPROVE? ──так──► IN_TEST ──► COMPLETED
//	   ▲            │                              │
//	   │            ні: review_attempts++          └──► FAILED
//	   │            │
//	   └── < max ───┤
//	                └── >= max ──► IN_TEST (форсовано, з попередженням)
//
// У PLAN_REVIEW Run виходить і задача стоїть, доки людина в адмін-панелі не
// натисне «Схвалити» (ApprovePlan) або не надішле правки (RevisePlan).
//
// Кожен етап — окрема функція, яка змінює task.Status. Після кожного етапу
// стан зберігається в PostgreSQL. Тому якщо контейнер впаде, задачу можна
// продовжити з того самого місця кнопкою «Продовжити» в адмін-панелі
// (POST /api/tasks/{id}/resume).
//
// Задачу можна зупинити (POST /api/tasks/{id}/stop): агента, що працює, м'яко
// завершуємо, статус лишається тим самим етапом. Id сесії claude зберігається
// в БД ще до старту агента, тож «Продовжити» відновлює ту саму сесію
// (claude --resume) — агент бачить усе, що встиг зробити, і доробляє етап.
package pipeline

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"orchestrator/internal/agent"
	"orchestrator/internal/config"
	"orchestrator/internal/jira"
	"orchestrator/internal/storage"
	"orchestrator/internal/workspace"
)

// Назви етапів для таблиці task_logs.
const (
	stepPlanning    = "PLANNING"
	stepPlanReview  = "PLAN_REVIEW" // рішення людини щодо плану
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
	WorktreeCopy    []string // gitignored-файли, які копіюються в кожен worktree
	TestEnvScript   string   // скрипт середовища для QA, відносно папки задачі
	TestEnvCleanup  string   // команда, яка прибирає середовище після тестування

	// testEnvMu — тестове середовище (кластер) одне на всіх: поки його
	// використовує QA однієї задачі, QA інших задач чекає.
	testEnvMu sync.Mutex

	// running — які задачі зараз виконуються (або видаляються) в цьому процесі:
	// ключ задачі → *runState. sync.Map — потокобезпечна мапа
	// (кілька goroutine можуть писати одночасно).
	running sync.Map
}

// runState — задача, яку зараз обробляє цей процес.
type runState struct {
	stop context.CancelCauseFunc // зупиняє Run; nil, якщо задачу видаляють
}

// ErrStopped — причина скасування контексту, коли задачу зупинили з адмін-панелі.
var ErrStopped = errors.New("задачу зупинено")

// IsRunning каже, чи задачу зараз виконує (або видаляє) цей процес.
func (p *Pipeline) IsRunning(key string) bool {
	_, ok := p.running.Load(key)
	return ok
}

// Stop зупиняє виконання задачі. Повертається одразу: агент отримує Ctrl+C
// і за кілька секунд завершується, після чого Run виходить, не змінюючи статус.
func (p *Pipeline) Stop(key string) error {
	v, ok := p.running.Load(key)
	if !ok {
		return fmt.Errorf("задача %s зараз не виконується", key)
	}
	st := v.(*runState)
	if st.stop == nil {
		return fmt.Errorf("%w: задачу %s саме видаляють", ErrRunning, key)
	}
	st.stop(ErrStopped)
	slog.Info("зупиняю задачу", "key", key)
	return nil
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
		return nil, fmt.Errorf("задача %s вже існує (статус %s). Щоб продовжити — відкрий її в адмін-панелі і натисни «Продовжити»",
			key, existing.Status)
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
	if p.IsRunning(key) {
		return nil, fmt.Errorf("%w: задача %s ще не зупинилась", ErrRunning, key)
	}
	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case storage.StatusCompleted:
		return nil, fmt.Errorf("задача %s вже виконана", key)
	case storage.StatusPlanReview:
		return nil, fmt.Errorf("%w: план задачі %s чекає на твоє рішення — схвали його або надішли правки",
			ErrWrongState, key)
	case storage.StatusFailed:
		// Дописуємо причину падіння до фідбеку (там може вже бути звіт QA).
		if task.LastError != "" {
			task.LastFeedback = strings.TrimSpace(task.LastFeedback +
				"\n\nПопередній запуск завершився помилкою:\n" + task.LastError)
		}
		task.Status = storage.StatusCreated // заново створимо worktree (на тій самій гілці)
		task.ReviewAttempts = 0
		task.LastError = ""
		task.AgentRole, task.AgentSession = "", "" // етап починаємо з нуля
		if err := p.Store.SaveTask(ctx, task); err != nil {
			return nil, err
		}
	}
	return task, nil
}

// Retest готує повторне тестування завершеної (або впалої) задачі: запускається
// лише QA-агент, з додатковими інструкціями людини (можна порожні).
// Якщо тести пройдуть — задача знову COMPLETED, якщо ні — FAILED зі звітом QA
// (тоді «Перезапустити» віддасть звіт розробнику).
// Саме тестування запускає Run (його викликає API після цього методу).
func (p *Pipeline) Retest(ctx context.Context, key, instructions string) (*storage.Task, error) {
	if _, busy := p.running.LoadOrStore(key, &runState{}); busy {
		return nil, fmt.Errorf("%w: задача %s зараз виконується", ErrRunning, key)
	}
	defer p.running.Delete(key)

	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return nil, err
	}
	if !task.Status.IsFinal() {
		return nil, fmt.Errorf("%w: перетестувати можна лише готову або впалу задачу (статус %s)", ErrWrongState, task.Status)
	}
	// Задача впала ще до розробки (на плануванні) — тестувати нічого.
	// Старим задачам міграція 006 проставила plan_approved_at, тож їх це не зачіпає.
	if task.PlanApprovedAt == nil {
		return nil, fmt.Errorf("%w: задача %s ще не дійшла до розробки — коду для тестування немає", ErrWrongState, key)
	}

	task.Status = storage.StatusInTest
	task.TestInstructions = strings.TrimSpace(instructions)
	task.LastError = ""
	task.CompletedAt = nil
	task.AgentRole, task.AgentSession = "", ""
	if err := p.Store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	slog.Info("перезапускаю тестування", "key", key, "instructions", task.TestInstructions != "")
	return task, nil
}

// ErrMerged — задачу не можна видалити: її коміти вже в main.
var ErrMerged = errors.New("задача вже в main")

// ErrRunning — задачу не можна видалити: її зараз виконують агенти.
var ErrRunning = errors.New("задача зараз виконується")

// ErrWrongState — дія не підходить до поточного статусу задачі
// (наприклад, схвалити план, який ще не готовий).
var ErrWrongState = errors.New("недоступно в поточному стані задачі")

// ApprovePlan — людина схвалила план: задача йде на розробку.
// Саму розробку запускає Run (його викликає API після цього методу).
func (p *Pipeline) ApprovePlan(ctx context.Context, key string) (*storage.Task, error) {
	return p.decidePlan(ctx, key, func(task *storage.Task) (string, string) {
		now := time.Now()
		task.PlanApprovedAt = &now
		task.PlanFeedback = ""
		task.Status = storage.StatusInDev
		return verdictPlanOK, fmt.Sprintf("План (версія %d) схвалено — задача пішла на розробку.", task.PlanRevision)
	})
}

// RevisePlan — людина написала правки до плану: Архітектор переробить його.
func (p *Pipeline) RevisePlan(ctx context.Context, key, comment string) (*storage.Task, error) {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return nil, errors.New("напиши, що саме треба змінити в плані")
	}
	return p.decidePlan(ctx, key, func(task *storage.Task) (string, string) {
		task.PlanFeedback = comment
		task.Status = storage.StatusInPlanning
		return verdictPlanChanges, fmt.Sprintf("Правки до плану (версія %d) — Архітектор його переробить.", task.PlanRevision)
	})
}

// decidePlan застосовує рішення людини до задачі, що чекає в PLAN_REVIEW,
// зберігає її і записує рішення в історію. decide змінює задачу і повертає
// вердикт та текст для хронології.
func (p *Pipeline) decidePlan(ctx context.Context, key string, decide func(*storage.Task) (verdict, summary string)) (*storage.Task, error) {
	// Займаємо задачу на час рішення — щоб два одночасні кліки
	// («Схвалити» і «Надіслати правки») не перезаписали одне одного.
	if _, busy := p.running.LoadOrStore(key, &runState{}); busy {
		return nil, fmt.Errorf("%w: задача %s саме обробляється", ErrRunning, key)
	}
	defer p.running.Delete(key)

	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return nil, err
	}
	if task.Status != storage.StatusPlanReview {
		return nil, fmt.Errorf("%w: задача %s не чекає на рішення щодо плану (статус %s)", ErrWrongState, key, task.Status)
	}

	verdict, summary := decide(task)
	if err := p.Store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	l := &storage.TaskLog{
		TaskID:    task.ID,
		StepName:  stepPlanReview,
		AgentRole: "operator",
		Verdict:   verdict,
		Summary:   summary,
	}
	if verdict == verdictPlanChanges {
		l.Feedback = task.PlanFeedback
	}
	if err := p.Store.AddLog(ctx, l); err != nil {
		slog.Error("не вдалося записати рішення щодо плану", "key", key, "error", err)
	}
	slog.Info("рішення щодо плану", "key", key, "verdict", verdict, "revision", task.PlanRevision)
	return task, nil
}

// Delete повністю видаляє задачу: worktree і гілку в кожному репозиторії
// (разом з комітами), журнал дій агентів і всі записи в базі.
//
// Якщо хоч в одному репозиторії коміти задачі вже потрапили в main —
// нічого не видаляємо і повертаємо ErrMerged: історію виконаної роботи
// прибирати не можна.
func (p *Pipeline) Delete(ctx context.Context, key string) error {
	// Займаємо задачу, як Run: поки видаляємо, її не можна запустити.
	if _, busy := p.running.LoadOrStore(key, &runState{}); busy {
		return fmt.Errorf("%w: зупини задачу %s, щоб її видалити", ErrRunning, key)
	}
	defer p.running.Delete(key)

	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return err
	}

	// Спершу перевіряємо всі репозиторії, і лише потім видаляємо —
	// щоб не лишити задачу видаленою наполовину.
	var merged []string
	for _, r := range task.Repos {
		into, err := p.Workspace.MergedInto(ctx, r.RepoName, r.BaseCommit, r.HeadCommit)
		if err != nil {
			return fmt.Errorf("перевірка мерджу в %s: %w", r.RepoName, err)
		}
		if into != "" {
			merged = append(merged, fmt.Sprintf("%s (%s)", r.RepoName, into))
		}
	}
	if len(merged) > 0 {
		return fmt.Errorf("%w: неможливо видалити %s — гілку %s замерджено в %s",
			ErrMerged, key, task.BranchName, strings.Join(merged, ", "))
	}

	for _, r := range task.Repos {
		if err := p.Workspace.Purge(ctx, r.RepoName, r.WorktreePath, task.BranchName); err != nil {
			return fmt.Errorf("видалення гілки в %s: %w", r.RepoName, err)
		}
	}
	// Папка задачі (/tmp/workspaces/<KEY>) і журнал дій агентів.
	_ = os.RemoveAll(filepath.Join(p.Workspace.WorktreesDir, key))
	if p.Runner != nil && p.Runner.LogDir != "" {
		_ = os.Remove(filepath.Join(p.Runner.LogDir, key+".log"))
	}

	if err := p.Store.DeleteTask(ctx, key); err != nil {
		return err
	}
	slog.Info("задачу видалено", "key", key)
	return nil
}

// Run виконує задачу від поточного статусу до фінального (COMPLETED/FAILED).
// Це і є головний цикл скінченного автомата.
func (p *Pipeline) Run(ctx context.Context, key string) error {
	// WithCancelCause — контекст, який Stop скасує з причиною ErrStopped.
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)

	// Захист від подвійного запуску однієї задачі в одному процесі.
	if _, alreadyRunning := p.running.LoadOrStore(key, &runState{stop: stop}); alreadyRunning {
		return fmt.Errorf("задача %s вже виконується", key)
	}
	defer p.running.Delete(key)

	task, err := p.Store.GetTask(ctx, key)
	if err != nil {
		return err
	}

	// IsWaiting: план чекає на людину — виходимо, продовжить ApprovePlan / RevisePlan.
	for !task.Status.IsFinal() && !task.Status.IsWaiting() {
		slog.Info("етап", "key", key, "status", task.Status, "review_attempts", task.ReviewAttempts)

		var stepErr error
		switch task.Status {
		case storage.StatusCreated:
			stepErr = p.prepare(ctx, task)
		case storage.StatusInPlanning:
			stepErr = p.plan(ctx, task)
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
				slog.Warn("виконання перервано, задачу можна продовжити", "key", key,
					"status", task.Status, "cause", context.Cause(ctx), "session", task.AgentSession)
				return context.Cause(ctx)
			}
			return p.fail(ctx, task, stepErr)
		}

		if err := p.Store.SaveTask(ctx, task); err != nil {
			return err
		}
	}

	if task.Status.IsWaiting() {
		slog.Info("план готовий і чекає на рішення в адмін-панелі", "key", key, "revision", task.PlanRevision)
		return nil
	}
	slog.Info("задачу завершено", "key", key, "status", task.Status)
	return nil
}

// ───────────────────────────── Етапи ─────────────────────────────

// prepare: CREATED → IN_PLANNING (або одразу IN_DEV, якщо план уже схвалено —
// наприклад, задачу перезапустили після падіння). Створюємо ізольований
// git worktree для кожного репозиторію задачі.
func (p *Pipeline) prepare(ctx context.Context, task *storage.Task) error {
	if err := p.createWorktrees(ctx, task); err != nil {
		return err
	}
	task.Status = storage.StatusInPlanning
	if task.PlanApprovedAt != nil {
		task.Status = storage.StatusInDev
	}
	return nil
}

// createWorktrees створює (або відкриває наявні) worktree для всіх репозиторіїв
// задачі на гілці задачі.
func (p *Pipeline) createWorktrees(ctx context.Context, task *storage.Task) error {
	// range по індексу: r — вказівник на елемент слайсу, тож зміни
	// потраплять у сам task.Repos (а не в його копію).
	for i := range task.Repos {
		r := &task.Repos[i]
		path, baseCommit, newBranch, err := p.Workspace.Create(ctx, r.RepoName, task.ID)
		if err != nil {
			return fmt.Errorf("worktree для %s: %w", r.RepoName, err)
		}
		r.WorktreePath = path
		if err := p.Workspace.CopyIgnored(r.RepoName, path, p.WorktreeCopy); err != nil {
			return fmt.Errorf("копіювання локальних файлів у worktree %s: %w", r.RepoName, err)
		}
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
	return nil
}

// plan: IN_PLANNING → PLAN_REVIEW. Architect досліджує код і пише план;
// далі задача чекає, поки людина його схвалить або напише правки.
func (p *Pipeline) plan(ctx context.Context, task *storage.Task) error {
	run, err := p.runAgent(ctx, task, "architect", architectPrompt(task))
	res := run.Result
	if err != nil {
		p.addLog(ctx, task, stepPlanning, "architect", 0, run, err, "", "")
		return err
	}

	// Архітектор має лише читати код. Якщо він усе ж щось змінив — скасовуємо,
	// щоб ці зміни не потрапили в коміт розробника. Планування буває лише до
	// схвалення плану, тобто ще до будь-якої роботи розробника.
	for _, r := range task.Repos {
		discarded, err := p.Workspace.DiscardChanges(ctx, r.WorktreePath)
		if err != nil {
			return fmt.Errorf("скасування змін архітектора в %s: %w", r.RepoName, err)
		}
		if discarded {
			slog.Warn("архітектор змінив файли — зміни скасовано", "key", task.ID, "repo", r.RepoName)
		}
	}

	plan := extractPlan(res.Text)
	if plan == "" {
		err := errors.New("архітектор не повернув план")
		p.addLog(ctx, task, stepPlanning, "architect", 0, run, err, "", "")
		return err
	}
	task.Plan = plan
	task.PlanRevision++
	task.PlanFeedback = ""
	p.addLog(ctx, task, stepPlanning, "architect", 0, run, nil, verdictPlanReady, "")
	task.Status = storage.StatusPlanReview
	return nil
}

// develop: IN_DEV → IN_REVIEW. Developer пише код, ми комітимо результат.
func (p *Pipeline) develop(ctx context.Context, task *storage.Task) error {
	iteration := task.ReviewAttempts + 1
	run, err := p.runAgent(ctx, task, "developer", developerPrompt(task))
	if err != nil {
		p.addLog(ctx, task, stepDevelopment, "developer", iteration, run, err, "", "")
		return err
	}

	msg := fmt.Sprintf("%s: %s", task.ID, task.Title)
	if task.ReviewAttempts > 0 {
		msg += fmt.Sprintf(" (виправлення після рев'ю #%d)", task.ReviewAttempts)
	}
	if err := p.commitAll(ctx, task, msg); err != nil {
		return err
	}
	if err := p.refreshRepoStats(ctx, task); err != nil {
		return err
	}

	p.addLog(ctx, task, stepDevelopment, "developer", iteration, run, nil, verdictDone, "")
	task.Status = storage.StatusInReview
	return nil
}

// review: IN_REVIEW → IN_TEST (схвалено або ліміт) або назад у IN_DEV.
func (p *Pipeline) review(ctx context.Context, task *storage.Task) error {
	iteration := task.ReviewAttempts + 1
	changed, err := p.changedRepos(ctx, task)
	if err != nil {
		return err
	}
	run, err := p.runAgent(ctx, task, "reviewer", reviewerPrompt(task, changed))
	res := run.Result

	// Читаємо review_feedback.md (якщо рев'юер його створив) і одразу
	// видаляємо, щоб файл не потрапив у коміт. Якщо рев'юера зупинили —
	// файл лишаємо: відновлена сесія допише його.
	feedbackPath := filepath.Join(task.WorktreePath, reviewFeedbackFile)
	feedbackBytes, _ := os.ReadFile(feedbackPath) // помилку ігноруємо: файлу може й не бути
	if ctx.Err() == nil {
		_ = os.Remove(feedbackPath)
	}
	feedback := strings.TrimSpace(string(feedbackBytes))

	if err != nil {
		p.addLog(ctx, task, stepReview, "reviewer", iteration, run, err, "", feedback)
		return err
	}

	if isApproved(res.Text) {
		slog.Info("рев'ю пройдено ✅", "key", task.ID)
		p.addLog(ctx, task, stepReview, "reviewer", iteration, run, nil, verdictApprove, feedback)
		task.LastFeedback = ""
		task.Status = storage.StatusInTest
		return nil
	}
	p.addLog(ctx, task, stepReview, "reviewer", iteration, run, nil, verdictChanges, feedback)

	// Зауваження: беремо з файлу, а якщо файлу немає — з відповіді агента.
	task.LastFeedback = feedback
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
	iteration := task.ReviewAttempts + 1
	// Після завершення задачі worktree видаляються. Якщо тестування
	// перезапустили («Перетестувати») — відкриваємо їх знову з гілки задачі.
	if err := p.createWorktrees(ctx, task); err != nil {
		return err
	}
	changed, err := p.changedRepos(ctx, task)
	if err != nil {
		return err
	}
	testEnv := p.testEnvScript(task)
	if testEnv != "" {
		p.testEnvMu.Lock()
		defer p.testEnvMu.Unlock()
		// defer виконуються у зворотному порядку: спершу прибираємо середовище,
		// потім відпускаємо його для QA наступної задачі.
		defer p.cleanupTestEnv(task)
	}
	run, err := p.runAgent(ctx, task, "tester", testerPrompt(task, changed, testEnv))
	res := run.Result
	instructions := task.TestInstructions // показуємо в хронології біля запуску
	if err != nil {
		p.addLog(ctx, task, stepTesting, "tester", iteration, run, err, "", instructions)
		return err
	}
	task.TestInstructions = "" // відпрацювали — наступне тестування вже без них

	// Тестувальник міг дописати тести — зберігаємо їх у гілці.
	if err := p.commitAll(ctx, task, task.ID+": тести від QA-агента"); err != nil {
		return err
	}
	if err := p.refreshRepoStats(ctx, task); err != nil {
		return err
	}
	task.TestReport = res.Text

	if !isTestPassed(res.Text) {
		p.addLog(ctx, task, stepTesting, "tester", iteration, run, nil, testFailed, instructions)
		// Звіт тестувальника стане фідбеком для Developer'а при resume.
		task.LastFeedback = "Звіт QA:\n" + res.Text
		return errors.New("тестування не пройдено, подробиці — у звіті QA")
	}

	p.addLog(ctx, task, stepTesting, "tester", iteration, run, nil, testPassed, instructions)
	p.cleanup(ctx, task)
	now := time.Now()
	task.CompletedAt = &now
	task.Status = storage.StatusCompleted
	return nil
}

// ───────────────────────────── Допоміжне ─────────────────────────────

// cleanupTestEnv прибирає тестове середовище після роботи QA — навіть якщо
// задачу зупинили, тому з власним контекстом.
func (p *Pipeline) cleanupTestEnv(task *storage.Task) {
	if p.TestEnvCleanup == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", p.TestEnvCleanup).CombinedOutput()
	if err != nil {
		slog.Warn("не вдалося прибрати тестове середовище", "key", task.ID, "error", err, "output", strings.TrimSpace(string(out)))
		return
	}
	slog.Info("тестове середовище прибрано", "key", task.ID)
}

// testEnvScript — повний шлях до скрипта тестового середовища в worktree задачі,
// або "", якщо скрипт не налаштовано чи його репозиторію немає серед репозиторіїв задачі.
func (p *Pipeline) testEnvScript(task *storage.Task) string {
	if p.TestEnvScript == "" {
		return ""
	}
	path := filepath.Join(p.Workspace.WorktreesDir, task.ID, p.TestEnvScript)
	if _, err := os.Stat(path); err != nil {
		slog.Warn("скрипт тестового середовища не знайдено — QA тестуватиме без нього", "key", task.ID, "path", path)
		return ""
	}
	return path
}

// AgentRoles — ролі агентів, які працюють над задачею (для налаштувань в адмін-панелі).
var AgentRoles = []string{"architect", "developer", "reviewer", "tester"}

// agentRun — один запуск агента: результат, завдання, яке він отримав насправді,
// і як він працював із сесією (storage.SessionNew / SessionResume / SessionContinue).
type agentRun struct {
	Result      *agent.Result
	Prompt      string
	SessionMode string
}

// runAgent запускає claude з налаштуваннями ролі. Сесію обирає так:
//
//  1. Етап уже починав агент цієї ролі, а потім задачу зупинили — відновлюємо
//     ту саму сесію з коротким «продовжуй» (continue).
//  2. Роль уже працювала над задачею раніше (наприклад, розробник після рев'ю),
//     і для ролі в налаштуваннях увімкнено «продовжувати сесію» — продовжуємо
//     її сесію (claude --resume) з новим завданням (resume).
//  3. Інакше — нова сесія (new).
//
// Id сесії записуємо в БД ще до старту агента: якщо процес впаде посеред роботи,
// сесію все одно можна буде відновити.
func (p *Pipeline) runAgent(ctx context.Context, task *storage.Task, role, prompt string) (*agentRun, error) {
	if task.AgentRole == role && task.AgentSession != "" {
		if run, ok, err := p.tryResume(ctx, task, role, resumePrompt, task.AgentSession, storage.SessionContinue); ok {
			return run, err
		}
	}

	setting := p.roleSetting(ctx, role)
	if setting.ResumeSession {
		session, err := p.Store.RoleSession(ctx, task.ID, role)
		if err != nil {
			return &agentRun{Prompt: prompt}, fmt.Errorf("читання сесії ролі %s: %w", role, err)
		}
		if session != "" {
			if err := p.rememberSession(ctx, task, role, session); err != nil {
				return &agentRun{Prompt: prompt}, err
			}
			if run, ok, err := p.tryResume(ctx, task, role, returnPrompt+prompt, session, storage.SessionResume); ok {
				return run, err
			}
		}
	}

	session := newSessionID()
	if err := p.rememberSession(ctx, task, role, session); err != nil {
		return &agentRun{Prompt: prompt}, err
	}
	if err := p.Store.SetRoleSession(ctx, task.ID, role, session); err != nil {
		return &agentRun{Prompt: prompt}, fmt.Errorf("збереження сесії ролі %s: %w", role, err)
	}
	res, err := p.launchAgent(ctx, task, role, prompt, session, false)
	if err == nil {
		task.AgentRole, task.AgentSession = "", ""
	}
	return &agentRun{Result: res, Prompt: prompt, SessionMode: storage.SessionNew}, err
}

// tryResume продовжує наявну сесію. ok = false — сесію не вдалося відкрити
// (наприклад, її файл видалили) і агент не зробив жодного кроку: тоді
// runAgent почне етап у новій сесії.
func (p *Pipeline) tryResume(ctx context.Context, task *storage.Task, role, prompt, session, mode string) (*agentRun, bool, error) {
	res, err := p.launchAgent(ctx, task, role, prompt, session, true)
	if err == nil || ctx.Err() != nil || (res != nil && res.NumTurns > 0) {
		if err == nil {
			task.AgentRole, task.AgentSession = "", ""
		}
		return &agentRun{Result: res, Prompt: prompt, SessionMode: mode}, true, err
	}
	slog.Warn("не вдалося відновити сесію агента, починаю етап у новій сесії",
		"key", task.ID, "role", role, "session", session, "mode", mode, "error", err)
	return nil, false, nil
}

// rememberSession записує, яка сесія виконує поточний етап, — щоб після
// зупинки задачі «Продовжити» відновило саме її.
func (p *Pipeline) rememberSession(ctx context.Context, task *storage.Task, role, session string) error {
	task.AgentRole, task.AgentSession = role, session
	if err := p.Store.SetAgentSession(ctx, task.ID, role, session); err != nil {
		return fmt.Errorf("збереження сесії агента: %w", err)
	}
	return nil
}

// roleSetting — налаштування ролі з адмін-панелі (продовжувати сесію, модель).
// Якщо їх не вдалося прочитати — значення за замовчуванням: продовжувати сесію,
// модель з config.yaml або storage.DefaultModel.
func (p *Pipeline) roleSetting(ctx context.Context, role string) storage.RoleSetting {
	settings, err := p.Store.RoleSettings(ctx, []string{role})
	if err != nil {
		slog.Warn("не вдалося прочитати налаштування ролі, беру значення за замовчуванням", "role", role, "error", err)
		model := p.Roles[role].Model
		if model == "" {
			model = storage.DefaultModel
		}
		return storage.RoleSetting{Role: role, ResumeSession: true, Model: model}
	}
	return settings[0]
}

// launchAgent запускає claude з налаштуваннями ролі.
func (p *Pipeline) launchAgent(ctx context.Context, task *storage.Task, role, prompt, session string, resume bool) (*agent.Result, error) {
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

	return p.Runner.Run(ctx, agent.Request{
		TaskID:            task.ID,
		Role:              role,
		WorkDir:           task.WorktreePath,
		Prompt:            prompt,
		SystemInstruction: roleCfg.SystemInstruction,
		Model:             p.roleSetting(ctx, role).Model, // обирається в адмін-панелі
		ExtraFlags:        flags,
		SessionID:         session,
		Resume:            resume,
	})
}

// newSessionID генерує випадковий UUID v4 — у такому форматі claude приймає --session-id.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // версія 4
	b[8] = b[8]&0x3f | 0x80 // варіант RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// addLog записує запуск агента в історію задачі (таблиця task_logs).
// Якщо агент завершився помилкою (runErr != nil), вердикт — ERROR,
// а якщо його зупинили (Ctrl+C / зупинка контейнера) — INTERRUPTED.
func (p *Pipeline) addLog(ctx context.Context, task *storage.Task, step, role string, iteration int,
	run *agentRun, runErr error, verdict, feedback string) {
	if runErr != nil {
		verdict = verdictError
		if ctx.Err() != nil {
			verdict = verdictInterrupted
		}
	}

	l := &storage.TaskLog{
		TaskID:      task.ID,
		StepName:    step,
		AgentRole:   role,
		Iteration:   iteration,
		Verdict:     verdict,
		Prompt:      run.Prompt,
		Feedback:    feedback,
		SessionMode: run.SessionMode,
	}
	if res := run.Result; res != nil {
		l.Summary = res.Text
		l.Brief = extractBrief(res.Text)
		l.CostUSD = res.CostUSD
		l.NumTurns = res.NumTurns
		l.DurationMS = res.Duration.Milliseconds()
		l.OutputLog = res.RawLog
	}
	if runErr != nil && l.Summary == "" {
		l.Summary = runErr.Error()
	}

	// WithoutCancel: навіть якщо задачу зупинили, запис в історію має дійти до БД.
	if err := p.Store.AddLog(context.WithoutCancel(ctx), l); err != nil {
		slog.Error("не вдалося записати лог", "key", task.ID, "error", err)
	}
}

// fail переводить задачу у FAILED, записує помилку та прибирає worktree.
func (p *Pipeline) fail(ctx context.Context, task *storage.Task, cause error) error {
	slog.Error("задача завершилась помилкою", "key", task.ID, "status", task.Status, "error", cause)

	// Зберігаємо незакомічену роботу в гілках, щоб нічого не загубилося.
	for _, r := range task.Repos {
		_, _ = p.Workspace.CommitAll(ctx, r.WorktreePath, task.ID+": WIP (задача завершилась помилкою)")
	}
	// Помилку ігноруємо: якщо задача впала ще до створення worktree, статистики немає.
	_ = p.refreshRepoStats(ctx, task)
	_ = p.Store.AddLog(ctx, &storage.TaskLog{
		TaskID:    task.ID,
		StepName:  stepError,
		AgentRole: "orchestrator",
		Iteration: task.ReviewAttempts + 1,
		Verdict:   verdictError,
		Summary:   cause.Error(),
	})
	p.cleanup(ctx, task)

	task.Status = storage.StatusFailed
	task.LastError = cause.Error()
	task.AgentRole, task.AgentSession = "", ""
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

// refreshRepoStats оновлює в task.Repos кількість комітів, останній коміт
// і diff --stat. Після етапу SaveTask запише це в БД — так історія змін
// лишається навіть після видалення worktree.
func (p *Pipeline) refreshRepoStats(ctx context.Context, task *storage.Task) error {
	for i := range task.Repos {
		r := &task.Repos[i]
		commits, head, diffStat, err := p.Workspace.Stats(ctx, r.WorktreePath, r.BaseCommit)
		if err != nil {
			return fmt.Errorf("статистика змін у %s: %w", r.RepoName, err)
		}
		r.Commits, r.HeadCommit, r.DiffStat = commits, head, diffStat
	}
	return nil
}

// changedRepos повертає репозиторії, у гілці яких є коміти задачі.
func (p *Pipeline) changedRepos(ctx context.Context, task *storage.Task) ([]storage.TaskRepo, error) {
	if err := p.refreshRepoStats(ctx, task); err != nil {
		return nil, err
	}
	var changed []storage.TaskRepo
	for _, r := range task.Repos {
		if r.Commits > 0 {
			changed = append(changed, r)
		}
	}
	return changed, nil
}
