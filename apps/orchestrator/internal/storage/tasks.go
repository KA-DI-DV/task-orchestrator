package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Status — стан задачі у скінченному автоматі (FSM).
//
// `type Status string` створює новий тип на основі string. Це дає
// безпеку: не можна випадково передати будь-який рядок туди, де
// очікується саме статус.
type Status string

const (
	StatusCreated    Status = "CREATED"
	StatusInPlanning Status = "IN_PLANNING" // Архітектор складає план
	StatusPlanReview Status = "PLAN_REVIEW" // план чекає на рішення людини
	StatusInDev      Status = "IN_DEV"
	StatusInReview   Status = "IN_REVIEW"
	StatusInTest     Status = "IN_TEST"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

// IsFinal каже, чи задача вже завершилась (успішно чи ні).
func (s Status) IsFinal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// IsWaiting каже, що задача чекає на людину: агенти нічого не роблять,
// доки план не схвалять або не напишуть до нього правки.
func (s Status) IsWaiting() bool {
	return s == StatusPlanReview
}

// Task — один рядок таблиці tasks.
//
// Тег `db:"..."` підказує pgx, яка колонка SQL відповідає якому полю,
// а `json:"..."` — як поле називатиметься у відповіді REST API.
type Task struct {
	ID                string    `db:"id" json:"id"`
	JiraURL           string    `db:"jira_url" json:"jira_url"`
	RepoName          string    `db:"repo_name" json:"repo_name"` // основний репозиторій
	Status            Status    `db:"status" json:"status"`
	BranchName        string    `db:"branch_name" json:"branch_name"`
	WorktreePath      string    `db:"worktree_path" json:"worktree_path"` // worktree основного репозиторію
	ReviewAttempts    int       `db:"review_attempts" json:"review_attempts"`
	MaxReviewAttempts int       `db:"max_review_attempts" json:"max_review_attempts"`
	LastFeedback      string    `db:"last_feedback" json:"last_feedback"`
	Title             string    `db:"title" json:"title"`
	Description       string    `db:"description" json:"description"`
	BaseCommit        string    `db:"base_commit" json:"base_commit"`
	LastError         string    `db:"last_error" json:"last_error"`
	TestReport        string    `db:"test_report" json:"test_report"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time `db:"updated_at" json:"updated_at"`
	// *time.Time — вказівник, бо значення може бути NULL (задача ще не завершена).
	CompletedAt *time.Time `db:"completed_at" json:"completed_at"`

	// Сесія агента поточного етапу (порожньо, якщо етап ще не почався
	// або вже завершився) — щоб після зупинки відновити її, а не почати заново.
	AgentRole    string `db:"agent_role" json:"agent_role"`
	AgentSession string `db:"agent_session" json:"agent_session"`

	// План від Архітектора (markdown) і рішення людини щодо нього.
	Plan         string `db:"plan" json:"plan"`
	PlanRevision int    `db:"plan_revision" json:"plan_revision"`
	PlanFeedback string `db:"plan_feedback" json:"plan_feedback"` // правки до поточної версії
	// NULL — план ще не схвалено: після (пере)запуску задача йде на планування.
	PlanApprovedAt *time.Time `db:"plan_approved_at" json:"plan_approved_at"`

	// Running — задачу зараз виконують агенти в цьому процесі. Не з БД:
	// заповнює API з пам'яті конвеєра.
	Running bool `db:"-" json:"running"`

	// Обчислювані поля (підзапити в taskColumns) — для списку задач.
	TotalCostUSD float64 `db:"total_cost_usd" json:"total_cost_usd"`
	ChangedRepos int     `db:"changed_repos" json:"changed_repos"`

	// Усі репозиторії задачі (таблиця task_repos). db:"-" — pgx не шукає
	// таку колонку в tasks, заповнюємо окремим запитом.
	Repos []TaskRepo `db:"-" json:"repos,omitempty"`
}

// TaskRepo — один репозиторій задачі: рядок таблиці task_repos.
type TaskRepo struct {
	RepoName     string `db:"repo_name" json:"repo_name"`
	WorktreePath string `db:"worktree_path" json:"worktree_path"`
	BaseCommit   string `db:"base_commit" json:"base_commit"`
	IsMain       bool   `db:"is_main" json:"is_main"`
	Commits      int    `db:"commits" json:"commits"`         // комітів задачі в гілці
	HeadCommit   string `db:"head_commit" json:"head_commit"` // останній коміт гілки
	DiffStat     string `db:"diff_stat" json:"diff_stat"`     // git diff --stat від base
}

// MainRepo повертає основний репозиторій задачі (або nil, якщо його немає).
func (t *Task) MainRepo() *TaskRepo {
	for i := range t.Repos {
		if t.Repos[i].IsMain {
			return &t.Repos[i]
		}
	}
	return nil
}

// TaskLog — один рядок таблиці task_logs: один запуск агента.
type TaskLog struct {
	ID         int64   `db:"id" json:"id"`
	TaskID     string  `db:"task_id" json:"task_id"`
	StepName   string  `db:"step_name" json:"step_name"`
	AgentRole  string  `db:"agent_role" json:"agent_role"`
	Iteration  int     `db:"iteration" json:"iteration"`
	Verdict    string  `db:"verdict" json:"verdict"`
	Prompt     string  `db:"prompt" json:"prompt"`
	Summary    string  `db:"summary" json:"summary"` // повна фінальна відповідь агента
	Brief      string  `db:"brief" json:"brief"`     // підсумок кроку простою мовою
	Feedback   string  `db:"feedback" json:"feedback"`
	CostUSD    float64 `db:"cost_usd" json:"cost_usd"`
	NumTurns   int     `db:"num_turns" json:"num_turns"`
	DurationMS int64   `db:"duration_ms" json:"duration_ms"`
	OutputLog  string  `db:"output_log" json:"output_log"` // журнал дій агента + stderr
	// SessionNew / SessionResume / SessionContinue ("" — у старих записах).
	SessionMode string    `db:"session_mode" json:"session_mode"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"` // коли запуск завершився
}

// ErrNotFound повертається, коли задачі з таким ключем немає.
// Перевіряти так: errors.Is(err, storage.ErrNotFound).
var ErrNotFound = errors.New("задачу не знайдено")

// taskColumns — список колонок для SELECT.
// COALESCE(x, ”) перетворює NULL на порожній рядок, щоб у Go
// можна було використовувати звичайний string замість *string.
const taskColumns = `
	id, jira_url, repo_name, status, branch_name, worktree_path,
	review_attempts, max_review_attempts,
	COALESCE(last_feedback, '') AS last_feedback,
	title, description, base_commit,
	COALESCE(last_error, '') AS last_error,
	test_report, created_at, updated_at, completed_at, agent_role, agent_session,
	plan, plan_revision, plan_feedback, plan_approved_at,
	(SELECT COALESCE(SUM(l.cost_usd), 0) FROM task_logs l WHERE l.task_id = tasks.id) AS total_cost_usd,
	(SELECT COUNT(*) FROM task_repos r WHERE r.task_id = tasks.id AND r.commits > 0) AS changed_repos`

// CreateTask додає нову задачу разом з її репозиторіями. Якщо задача
// з таким ID вже існує — повертає помилку (щоб випадково не запустити
// ту саму задачу двічі).
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	// pgx.BeginFunc: транзакція, яка сама робить Commit, якщо функція
	// повернула nil, і Rollback — якщо помилку.
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tasks (id, jira_url, repo_name, status, branch_name, worktree_path,
			                   max_review_attempts, title, description)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			t.ID, t.JiraURL, t.RepoName, t.Status, t.BranchName, t.WorktreePath,
			t.MaxReviewAttempts, t.Title, t.Description,
		)
		if err != nil {
			return err
		}
		return saveRepos(ctx, tx, t)
	})
	if err != nil {
		return fmt.Errorf("створення задачі %s: %w", t.ID, err)
	}
	return nil
}

// GetTask шукає задачу за ключем (наприклад "PROJ-123").
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	// CollectExactlyOneRow + RowToAddrOfStructByName: pgx сам розкладе
	// колонки по полях структури Task, орієнтуючись на теги db:"...".
	task, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[Task])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// Основний репозиторій — першим, решта за алфавітом.
	rows, err = s.pool.Query(ctx, `
		SELECT repo_name, worktree_path, base_commit, is_main, commits, head_commit, diff_stat
		FROM task_repos WHERE task_id = $1 ORDER BY is_main DESC, repo_name`, id)
	if err != nil {
		return nil, err
	}
	task.Repos, err = pgx.CollectRows(rows, pgx.RowToStructByName[TaskRepo])
	return task, err
}

// ListTasks повертає останні задачі (найновіші — першими).
func (s *Store) ListTasks(ctx context.Context, limit int) ([]*Task, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Task])
}

// SaveTask зберігає всі змінні поля задачі (статус, лічильники, фідбек...)
// і стан її репозиторіїв.
func (s *Store) SaveTask(ctx context.Context, t *Task) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE tasks SET
				status = $2,
				worktree_path = $3,
				review_attempts = $4,
				last_feedback = NULLIF($5, ''),
				title = $6,
				description = $7,
				base_commit = $8,
				last_error = NULLIF($9, ''),
				test_report = $10,
				completed_at = $11,
				agent_role = $12,
				agent_session = $13,
				plan = $14,
				plan_revision = $15,
				plan_feedback = $16,
				plan_approved_at = $17,
				updated_at = now()
			WHERE id = $1`,
			t.ID, t.Status, t.WorktreePath, t.ReviewAttempts, t.LastFeedback,
			t.Title, t.Description, t.BaseCommit, t.LastError,
			t.TestReport, t.CompletedAt, t.AgentRole, t.AgentSession,
			t.Plan, t.PlanRevision, t.PlanFeedback, t.PlanApprovedAt,
		)
		if err != nil {
			return err
		}
		return saveRepos(ctx, tx, t)
	})
	if err != nil {
		return fmt.Errorf("збереження задачі %s: %w", t.ID, err)
	}
	return nil
}

// SetAgentSession одразу записує сесію агента, який стартує, — ще до кінця
// етапу: якщо процес впаде посеред роботи, сесію все одно можна буде відновити.
func (s *Store) SetAgentSession(ctx context.Context, id, role, session string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tasks SET agent_role = $2, agent_session = $3, updated_at = now() WHERE id = $1`,
		id, role, session)
	return err
}

// saveRepos записує репозиторії задачі: новий — додає, наявний — оновлює
// ("upsert" через ON CONFLICT ... DO UPDATE).
func saveRepos(ctx context.Context, tx pgx.Tx, t *Task) error {
	for _, r := range t.Repos {
		_, err := tx.Exec(ctx, `
			INSERT INTO task_repos (task_id, repo_name, worktree_path, base_commit, is_main,
			                        commits, head_commit, diff_stat)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (task_id, repo_name) DO UPDATE SET
				worktree_path = EXCLUDED.worktree_path,
				base_commit   = EXCLUDED.base_commit,
				is_main       = EXCLUDED.is_main,
				commits       = EXCLUDED.commits,
				head_commit   = EXCLUDED.head_commit,
				diff_stat     = EXCLUDED.diff_stat`,
			t.ID, r.RepoName, r.WorktreePath, r.BaseCommit, r.IsMain,
			r.Commits, r.HeadCommit, r.DiffStat)
		if err != nil {
			return fmt.Errorf("репозиторій %s: %w", r.RepoName, err)
		}
	}
	return nil
}

// AddLog записує один запуск агента в історію задачі.
func (s *Store) AddLog(ctx context.Context, l *TaskLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO task_logs (task_id, step_name, agent_role, iteration, verdict, prompt,
		                       summary, brief, feedback, cost_usd, num_turns, duration_ms, output_log,
		                       session_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		l.TaskID, l.StepName, l.AgentRole, l.Iteration, l.Verdict, l.Prompt,
		l.Summary, l.Brief, l.Feedback, l.CostUSD, l.NumTurns, l.DurationMS, l.OutputLog,
		l.SessionMode)
	return err
}

// ListLogs повертає всі логи задачі в хронологічному порядку.
func (s *Store) ListLogs(ctx context.Context, taskID string) ([]*TaskLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, step_name, agent_role, iteration, verdict, prompt, summary, brief, feedback,
		       cost_usd, num_turns, duration_ms, COALESCE(output_log, '') AS output_log, created_at, session_mode
		FROM task_logs WHERE task_id = $1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[TaskLog])
}

// DeleteTask видаляє задачу. Її логи й репозиторії (task_logs, task_repos)
// видаляються разом з нею завдяки ON DELETE CASCADE.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("видалення задачі %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
