-- Короткий підсумок кроку простою мовою (розділ «Підсумок простою мовою»
-- з відповіді агента) — показується в хронології адмін-панелі.
ALTER TABLE task_logs
    ADD COLUMN IF NOT EXISTS brief TEXT NOT NULL DEFAULT '';
