-- Остання сесія Claude Code кожної ролі в межах задачі. Коли задача повертається
-- до ролі (рев'юер → розробник, правки до плану → архітектор), її сесію
-- продовжують (claude --resume), якщо для ролі це ввімкнено в role_settings.
CREATE TABLE IF NOT EXISTS task_sessions (
    task_id    VARCHAR(64) NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    role       VARCHAR(32) NOT NULL,            -- architect / developer / reviewer / tester
    session_id VARCHAR(64) NOT NULL,            -- session id claude
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, role)
);

-- Налаштування ролей з адмін-панелі (однакові для всіх задач).
-- Немає рядка для ролі — діє значення за замовчуванням (продовжувати сесію).
CREATE TABLE IF NOT EXISTS role_settings (
    role           VARCHAR(32) PRIMARY KEY,
    resume_session BOOLEAN NOT NULL DEFAULT TRUE, -- true: claude --resume своєї сесії; false: щоразу нова сесія
    updated_at     TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Як агент працював у цьому запуску: new — нова сесія, resume — продовжив свою
-- сесію (задача повернулась до ролі), continue — відновив після зупинки.
ALTER TABLE task_logs
    ADD COLUMN IF NOT EXISTS session_mode VARCHAR(16) NOT NULL DEFAULT '';
