# ─────────────── Етап 1: збірка Go-бінарника ───────────────
# Multi-stage build: компілюємо в "важкому" образі з Go,
# а у фінальний образ копіюємо лише готовий бінарник.
FROM golang:1.27-trixie AS builder

WORKDIR /src
# Спочатку лише go.mod/go.sum — Docker закешує завантажені залежності,
# і при зміні коду вони не качатимуться заново.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/orchestrator ./cmd/orchestrator

# ─────────────── Етап 2: робочий образ ───────────────
# Node.js потрібен для Claude Code CLI та Playwright MCP.
FROM node:24-trixie-slim

# git — для worktree; chromium — для Browser MCP (пакет сам тягне libnss3 та інші залежності).
RUN apt-get update && apt-get install -y --no-install-recommends \
        git \
        curl \
        ca-certificates \
        chromium \
        fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

# Claude Code CLI + Playwright MCP (щоб не качати його через npx при кожному запуску).
RUN npm install -g @anthropic-ai/claude-code @playwright/mcp

# Jira MCP (mcp-atlassian) — Python-пакет. Ставимо через uv: він сам завантажить
# потрібний Python. Усе кладемо в /opt, а команду — у /usr/local/bin,
# щоб вона була доступна користувачу node.
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/
ENV UV_TOOL_DIR=/opt/uv/tools \
    UV_TOOL_BIN_DIR=/usr/local/bin \
    UV_PYTHON_INSTALL_DIR=/opt/uv/python
RUN uv tool install mcp-atlassian && chmod -R a+rX /opt/uv

# CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1 — claude підхоплює CLAUDE.md і з папок,
# доданих через --add-dir (інші репозиторії задачі).
ENV PUPPETEER_SKIP_CHROMIUM_DOWNLOAD=true \
    PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    CONFIG_PATH=/app/config.yaml \
    CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1

# Git: автор комітів за замовчуванням + дозвіл працювати з репозиторіями,
# змонтованими з Mac (у них інший власник файлів).
# Справжнє ім'я автора задається в .env (GIT_AUTHOR_* / GIT_COMMITTER_*) —
# git віддає цим змінним перевагу над git config.
RUN git config --system user.name "AI Orchestrator" \
 && git config --system user.email "orchestrator@localhost" \
 && git config --system --add safe.directory '*'

WORKDIR /app
COPY --from=builder /out/orchestrator /usr/local/bin/orchestrator
COPY config.yaml /app/config.yaml
COPY mcp /app/mcp

# Claude Code забороняє --dangerously-skip-permissions під root,
# тому працюємо від звичайного користувача node (є в образі node).
RUN mkdir -p /tmp/workspaces /home/node/.claude && chown -R node:node /tmp/workspaces /home/node/.claude
USER node

EXPOSE 8080
CMD ["orchestrator", "serve"]
