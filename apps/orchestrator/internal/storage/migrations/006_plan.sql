-- План від Архітектора. Задача чекає в PLAN_REVIEW, поки людина не схвалить
-- план або не напише правки (тоді Архітектор переробляє його).
ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS plan             TEXT NOT NULL DEFAULT '', -- поточна версія плану (markdown)
    ADD COLUMN IF NOT EXISTS plan_revision    INT  NOT NULL DEFAULT 0,  -- номер версії плану (1, 2, ...)
    ADD COLUMN IF NOT EXISTS plan_feedback    TEXT NOT NULL DEFAULT '', -- правки людини, які треба врахувати в наступній версії
    ADD COLUMN IF NOT EXISTS plan_approved_at TIMESTAMP WITH TIME ZONE; -- коли план схвалено (NULL — ще ні)

-- Задачі, створені до появи Архітектора, уже пройшли цей етап — вважаємо їхній «план» схваленим,
-- щоб після «Перезапустити» вони не пішли на планування.
UPDATE tasks SET plan_approved_at = created_at WHERE status <> 'CREATED';
