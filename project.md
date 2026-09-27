Technical Specification: Go Orchestrator Engine (Claude Code CLI + Jira + PostgreSQL)Даний документ є повною технічною специфікацією для розробки автономного оркестратора задач на мові Go. Система призначена для запуску на macOS у Docker-середовищі з використанням PostgreSQL як персистентного сховища та Claude Code CLI як універсального виконавчого рушія для AI-агентів.1. Загальна Архітектура СистемиОркестратор працює за принципом On-Demand (за запитом користувача). Користувач передає посилання або ключ задачі Jira (наприклад, PROJ-123). Оркестратор парсить запит, зчитує деталі тикета через Jira MCP / REST API, створює ізольований git worktree та проводити задачу через динамічний конвеєр агентів (Developer -> Reviewer -> QA). +-------------------------------------------------------------------+
 |                          USER INTERFACE                           |
 |               Command: orchestrator run <jira-url>                |
 +-------------------------------------------------------------------+
                                   |
                                   v
 +-------------------------------------------------------------------+
 |                         GO ORCHESTRATOR                           |
 |  +--------------------+   +-------------------+   +------------+  |
 |  | Task Ingestion     |   | FSM Engine        |   | Worktree   |  |
 |  | (Jira MCP/API)     |   | (Anti-Loop Guard) |   | Manager    |  |
 |  +--------------------+   +-------------------+   +------------+  |
 +-------------------------------------------------------------------+
       |                                   |                 |
       v                                   v                 v
+--------------+               +-----------------------+   +----------------------+
|  PostgreSQL  |               |  Claude Code CLI      |   | Workspace            |
|  Database    |               |  Execution Engine     |   | (/workspaces/repo/   |
|  (State/Logs)|               |                       |   |  worktree-PROJ-123)  |
+--------------+               +-----------------------+   +----------------------+
                                   |         |        |
                         +---------+         |        +----------+
                         |                   |                   |
                         v                   v                   v
                +------------------+ +------------------+ +------------------+
                | Agent: DEVELOPER | | Agent: REVIEWER  | | Agent: QA        |
                | - Code rules     | | - Code review    | | - Unit tests     |
                | - Local MCPs     | | - Feedback loop  | | - Browser MCP    |
                +------------------+ +------------------+ +------------------+
2. Ключові Функціональні ВимогиЗапуск За Запитом (On-Demand Execution):Відсутність фонового сканнера/поллера.Запуск виконується чітко за ініціативою користувача через CLI команду або REST ендпоінт.Персистентність Станiв у PostgreSQL:Збереження історії виконання, лічильників ітерацій рев'ю, текстових фідбеків та логів у базі даних PostgreSQL.При перезапуску Docker-контейнера стан задач не втрачається.Ізоляція Робочих Просторів (Git Worktrees):Кожна задача виконується у власному git worktree.Жодних конфліктів файлів при одночасній обробці кількох задач в одному й тому самому репозиторії.Автономність Агентів (Claude Code CLI Integration):Оркестратор запускає claude CLI як підпроцес у директорії відповідного worktree.Claude Code CLI автоматично підтягує інструкції, ролі, .cursorrules, CLAUDE.md та локальні MCP-сервери, прописані в цільовому репозиторії.Контроль Циклів (Anti-Loop Protection):Ліміт на кількість ітерацій Developer <-> Reviewer.При досягненні ліміту рев'ю задача форсовано передається на етап QA або ескалюється.Тестування через Browser MCP:Агент-тестувальник використовує підключений Playwright/Puppeteer MCP для автоматичного проклікування та перевірки веб-інтерфейсу в headless-режимі.3. Технологічний СтекМова програмування: Go 1.22+База даних: PostgreSQL 16Двигун агентів: Claude Code CLI (Anthropic)Інтеграційні протоколи: MCP (Model Context Protocol) — Jira MCP, Browser MCPСистема ізоляції: Git WorktreesКонтейнеризація: Docker, Docker Compose (на базі Debian/Bookworm для сумісності з Chromium)4. Конфігурація Проєкту (config.yaml)server:
  port: 8080

database:
  host: "postgres"
  port: 5432
  user: "orchestrator"
  password: "orchestrator_secret_password"
  dbname: "orchestrator_db"
  sslmode: "disable"

orchestrator:
  repos_base_dir: "/workspaces"
  worktrees_dir: "/tmp/workspaces"
  max_review_cycles: 3

jira:
  base_url: "https://your-domain.atlassian.net"
  use_mcp: true

roles:
  developer:
    cli_flags: ["--dangerously-skip-permissions"]
    system_instruction: "Дотримуйся локальних правил репозиторію. Напиши код та юніт-тести."

  reviewer:
    cli_flags: ["--dangerously-skip-permissions"]
    system_instruction: "Перевір git diff. Якщо все чудово — напиши 'APPROVE'. Якщо є зауваження — згенеруй review_feedback.md."

  tester:
    cli_flags: ["--dangerously-skip-permissions"]
    system_instruction: "Запусти локальні тести та використай Browser MCP для перевірки UI сценаріїв."
5. Схема Бази Даних (PostgreSQL DDL)-- Таблиця задач
CREATE TABLE tasks (
    id VARCHAR(64) PRIMARY KEY,                   -- Key задачи (напр. PROJ-123)
    jira_url TEXT NOT NULL,                       -- Передане посилання
    repo_name VARCHAR(255) NOT NULL,              -- Цільовий репозиторій
    status VARCHAR(32) NOT NULL,                  -- CREATED, IN_DEV, IN_REVIEW, IN_TEST, COMPLETED, FAILED
    branch_name VARCHAR(255) NOT NULL,            -- Git гілка (feature/PROJ-123)
    worktree_path TEXT NOT NULL,                  -- Шлях до worktree
    review_attempts INT DEFAULT 0,                -- Поточна кількість спроб рев'ю
    max_review_attempts INT DEFAULT 3,            -- Гранична кількість спроб
    last_feedback TEXT,                          -- Останній фідбек рев'юера
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Таблиця логів виконання агентів
CREATE TABLE task_logs (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) REFERENCES tasks(id) ON DELETE CASCADE,
    step_name VARCHAR(32) NOT NULL,               -- DEVELOPMENT, REVIEW, TESTING
    agent_role VARCHAR(32) NOT NULL,              -- developer, reviewer, tester
    output_log TEXT,                              -- Сирий лог роботи Claude Code CLI
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Індекси для прискорення вибірок
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_task_logs_task_id ON task_logs(task_id);
6. Деталізований Алгоритм Життєвого Циклу Задачі[User Input] 
     │
     ▼
[CREATED] ──► Parse Jira URL -> Pull Context via Jira MCP -> Create Git Worktree
     │
     ▼
[IN_DEV]  ──► Run Claude Code CLI (Developer Prompt + Repo Skills/MCPs)
     │
     ▼
[IN_REVIEW]► Run Claude Code CLI (Reviewer Prompt)
     │
     ├─► Approved? ──► YES ──────────────────────────┐
     │                                               │
     └─► NO ──► review_attempts++                    │
                 │                                   │
                 ├─► < max_review_attempts ──► [IN_DEV] (with review_feedback.md)
                 │                                   │
                 └─► >= max_review_attempts ─────────┤ (Force Escalate/Proceed)
                                                     │
                                                     ▼
[IN_TEST] ◄──────────────────────────────────────────┘
     │
     ▼
Run Claude Code CLI (Tester Prompt + Browser MCP + Local Tests)
     │
     ├─► Success ──► Update Jira via MCP ──► Clean Worktree ──► [COMPLETED]
     │
     └─► Failure ──► Log Errors ──────────► Clean Worktree ──► [FAILED]
7. Контейнеризація та Конфігурація DockerDockerfileFROM golang:1.22-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o orchestrator ./cmd/orchestrator

FROM node:20-bookworm-slim

# Встановлення Chromium та залежностей для Playwright / Browser MCP
RUN apt-get update && apt-get install -y \
    git \
    curl \
    ca-certificates \
    chromium \
    libnss3 \
    libatk-bridge2.0-0 \
    libxcomposite1 \
    libxrandr2 \
    libgbm1 \
    libasound2 \
    && rm -rf /var/lib/apt/lists/*

# Налаштування headless режиму для Chromium
ENV PUPPETEER_SKIP_CHROMIUM_DOWNLOAD=true \
    PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium

# Встановлення Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

WORKDIR /app
COPY --from=builder /app/orchestrator /app/orchestrator

EXPOSE 8080
CMD ["/app/orchestrator"]
docker-compose.ymlversion: '3.8'

services:
  postgres:
    image: postgres:16-alpine
    container_name: orchestrator_postgres
    environment:
      POSTGRES_USER: orchestrator
      POSTGRES_PASSWORD: orchestrator_secret_password
      POSTGRES_DB: orchestrator_db
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U orchestrator -d orchestrator_db"]
      interval: 5s
      timeout: 5s
      retries: 5

  orchestrator:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: orchestrator_app
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://orchestrator:orchestrator_secret_password@postgres:5432/orchestrator_db?sslmode=disable
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
    volumes:
      # Прокидання репозиторіїв з хоста (Mac)
      - /Users/yourname/Projects:/workspaces
      # Прокидання конфігів Claude та авторизації
      - ~/.claude:/root/.claude
    network_mode: "bridge"

volumes:
  postgres_data:
8. Дорожня Карта Реалізації (Implementation Roadmap)Етап 1: Базовий Каркас та Доступ до БД[ ] Створення Go-модуля, підключення gorm або jackc/pgx для PostgreSQL.[ ] Реалізація автоматичних міграцій схеми БД (tasks та task_logs).[ ] Створення CLI / REST ендпоінту для прийому команди orchestrator run <jira-url>.Етап 2: Робота з Git Worktrees та Jira MCP[ ] Парсер Jira URL для отримання ключа задачі.[ ] Створення модуля WorkspaceManager (виконання git worktree add та git worktree remove).[ ] Інтеграція з Jira MCP / REST API для витягування опису задачі та зчитування цільового репозиторій.Етап 3: Runner для Claude Code CLI та FSM[ ] Реалізація модуля AgentRunner для виконання claude CLI підпроцесів з можливістю захоплення stdout/stderr та передачі інструкцій.[ ] Реалізація FSM (Finite State Machine) для керування станами IN_DEV -> IN_REVIEW -> IN_TEST.[ ] Логіка лічильника review_attempts у PostgreSQL для захисту від нескінченного циклу.Етап 4: Docker & Browser MCP[ ] Збірка Docker-образу з Go, Node.js, Claude Code CLI та Chromium.[ ] Налаштування та тестування Browser MCP для етапу тестування.[ ] Налаштування docker-compose.yml з PostgreSQL та монтажем локальних папок.
