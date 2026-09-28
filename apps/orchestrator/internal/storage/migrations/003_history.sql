-- Історія роботи задачі для адмін-панелі (замість коментаря в Jira).

-- Кожен запуск агента: що він отримав, що відповів, вердикт і скільки це коштувало.
ALTER TABLE task_logs
    ADD COLUMN IF NOT EXISTS iteration   INT NOT NULL DEFAULT 0,          -- ітерація Developer ↔ Reviewer (з 1)
    ADD COLUMN IF NOT EXISTS verdict     VARCHAR(32) NOT NULL DEFAULT '', -- DONE / APPROVE / CHANGES_REQUESTED / PASSED / FAILED / ERROR / INTERRUPTED
    ADD COLUMN IF NOT EXISTS prompt      TEXT NOT NULL DEFAULT '',        -- завдання, яке отримав агент
    ADD COLUMN IF NOT EXISTS summary     TEXT NOT NULL DEFAULT '',        -- фінальна відповідь агента
    ADD COLUMN IF NOT EXISTS feedback    TEXT NOT NULL DEFAULT '',        -- вміст review_feedback.md
    ADD COLUMN IF NOT EXISTS cost_usd    DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS num_turns   INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS duration_ms BIGINT NOT NULL DEFAULT 0;

-- Що задача змінила в кожному репозиторії (worktree після завершення видаляється,
-- тому зберігаємо підсумок у базі).
ALTER TABLE task_repos
    ADD COLUMN IF NOT EXISTS commits     INT NOT NULL DEFAULT 0,          -- комітів задачі в гілці
    ADD COLUMN IF NOT EXISTS head_commit VARCHAR(64) NOT NULL DEFAULT '', -- останній коміт гілки
    ADD COLUMN IF NOT EXISTS diff_stat   TEXT NOT NULL DEFAULT '';        -- git diff --stat від base

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS test_report  TEXT NOT NULL DEFAULT '',       -- звіт QA-агента
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMP WITH TIME ZONE;
