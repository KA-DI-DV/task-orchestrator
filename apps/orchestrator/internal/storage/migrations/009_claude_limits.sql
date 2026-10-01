-- Останні відомі ліміти підписки Claude (подія rate_limit_event з stream-json claude):
-- скільки використано за 5 годин / тиждень і коли ліміт скинеться. Один рядок.
CREATE TABLE IF NOT EXISTS claude_limits (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    info        JSONB NOT NULL,                              -- rate_limit_info як є
    observed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);
