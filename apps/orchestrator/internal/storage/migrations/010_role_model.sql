-- Модель Claude для кожної ролі, яку обирають в адмін-панелі («Налаштування»).
-- Порожньо — модель за замовчуванням (storage.DefaultModel).
ALTER TABLE role_settings
    ADD COLUMN IF NOT EXISTS model VARCHAR(64) NOT NULL DEFAULT '';
