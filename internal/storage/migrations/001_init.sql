-- Таблиця задач
CREATE TABLE IF NOT EXISTS tasks (
    id                  VARCHAR(64) PRIMARY KEY,      -- Ключ задачі (напр. PROJ-123)
    jira_url            TEXT NOT NULL,                -- Передане посилання
    repo_name           VARCHAR(255) NOT NULL,        -- Цільовий репозиторій
    status              VARCHAR(32) NOT NULL,         -- CREATED, IN_DEV, IN_REVIEW, IN_TEST, COMPLETED, FAILED
    branch_name         VARCHAR(255) NOT NULL,        -- Git гілка (feature/PROJ-123)
    worktree_path       TEXT NOT NULL,                -- Шлях до worktree
    review_attempts     INT NOT NULL DEFAULT 0,       -- Поточна кількість спроб рев'ю
    max_review_attempts INT NOT NULL DEFAULT 3,       -- Гранична кількість спроб
    last_feedback       TEXT,                         -- Останній фідбек рев'юера

    -- Додаткові поля (яких не було в специфікації), потрібні, щоб задачу
    -- можна було продовжити після перезапуску без повторного запиту в Jira:
    title               TEXT NOT NULL DEFAULT '',     -- Заголовок задачі з Jira
    description         TEXT NOT NULL DEFAULT '',     -- Опис задачі з Jira
    base_commit         VARCHAR(64) NOT NULL DEFAULT '', -- Коміт, від якого створено гілку (для git diff)
    last_error          TEXT,                         -- Текст помилки, якщо задача впала

    created_at          TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Таблиця логів виконання агентів
CREATE TABLE IF NOT EXISTS task_logs (
    id          BIGSERIAL PRIMARY KEY,
    task_id     VARCHAR(64) REFERENCES tasks(id) ON DELETE CASCADE,
    step_name   VARCHAR(32) NOT NULL,                 -- DEVELOPMENT, REVIEW, TESTING
    agent_role  VARCHAR(32) NOT NULL,                 -- developer, reviewer, tester
    output_log  TEXT,                                 -- Сирий лог роботи Claude Code CLI
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Індекси для прискорення вибірок
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_task_logs_task_id ON task_logs(task_id);
