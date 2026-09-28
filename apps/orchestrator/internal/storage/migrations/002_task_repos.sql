-- Задача може зачіпати кілька репозиторіїв: для кожного — свій worktree
-- і свій коміт, від якого відгалузилася гілка задачі.
CREATE TABLE IF NOT EXISTS task_repos (
    task_id       VARCHAR(64) REFERENCES tasks(id) ON DELETE CASCADE,
    repo_name     VARCHAR(255) NOT NULL,             -- Назва репозиторію в repos_base_dir
    worktree_path TEXT NOT NULL,                     -- Шлях до worktree цього репозиторію
    base_commit   VARCHAR(64) NOT NULL DEFAULT '',   -- Коміт, від якого створено гілку (для git diff)
    is_main       BOOLEAN NOT NULL DEFAULT false,    -- Основний репозиторій: у ньому запускається claude
    PRIMARY KEY (task_id, repo_name)
);

-- Задачі, створені до цієї міграції, мали лише один репозиторій.
INSERT INTO task_repos (task_id, repo_name, worktree_path, base_commit, is_main)
SELECT id, repo_name, worktree_path, base_commit, true FROM tasks
ON CONFLICT DO NOTHING;
