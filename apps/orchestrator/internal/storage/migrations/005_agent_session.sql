-- Сесія Claude Code агента, який зараз виконує етап задачі. Якщо задачу
-- зупинили посеред етапу, «Продовжити» відновлює саме цю сесію (claude --resume),
-- а не починає етап з нуля. Після успішного етапу поля очищуються.
ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS agent_role    VARCHAR(32) NOT NULL DEFAULT '', -- developer / reviewer / tester
    ADD COLUMN IF NOT EXISTS agent_session VARCHAR(64) NOT NULL DEFAULT ''; -- session id claude
