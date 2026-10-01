-- Додаткові інструкції для QA-агента, коли тестування перезапускають з адмін-панелі
-- («Перетестувати»). Очищуються після того, як тестувальник відпрацював.
ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS test_instructions TEXT NOT NULL DEFAULT '';
