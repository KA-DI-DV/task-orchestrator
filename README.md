# Orchestrator

Оркестратор AI-агентів: береш задачу з Jira, і вона проходить конвеєр
**Developer → Reviewer → QA** у Claude Code CLI. Кожна задача отримує власні git worktree
всіх репозиторіїв (крім `exclude_repos`), бо реалізація може зачіпати кілька з них.
Стан зберігається в PostgreSQL.

```
Адмінка → «Нова задача» PROJ-123
  │
  ├─ CREATED    читає задачу з Jira, створює worktree + гілку feature/PROJ-123
  │             у кожному репозиторії: /tmp/workspaces/PROJ-123/<repo>
  ├─ IN_DEV     claude (developer) запускається в main_repo, решта — через --add-dir;
  │             пише код → оркестратор комітить у кожному репозиторії
  ├─ IN_REVIEW  claude (reviewer) дивиться git diff змінених репозиторіїв → APPROVE або зауваження
  │               └─ зауваження → назад в IN_DEV (максимум max_review_cycles разів)
  ├─ IN_TEST    claude (tester) запускає тести + Browser MCP (headless Chromium)
  └─ COMPLETED  коментар у Jira, worktree видаляються (гілки з комітами лишаються,
     або FAILED    порожні — видаляються)
```

---

## Структура репозиторію

Монорепо: кожен застосунок — окрема папка в `apps/` зі своїм Dockerfile і залежностями.
Усе, що стосується всього проєкту разом, лежить у корені.

```
.
├── apps/                          # Усі застосунки
│   ├── orchestrator/              # Go-сервіс: конвеєр агентів + REST API
│   │   ├── cmd/orchestrator/      # точка входу (main.go)
│   │   ├── internal/              # логіка: pipeline, agent, storage, jira, ...
│   │   ├── mcp/                   # конфіги MCP-серверів (Browser, Jira)
│   │   ├── config.yaml            # налаштування оркестратора
│   │   ├── Dockerfile
│   │   └── go.mod                 # власний go.mod
│   │
│   └── admin/                     # React-адмінка (Vite): звідси запускаються задачі
│       ├── src/
│       ├── Dockerfile
│       ├── package.json
│       └── vite.config.ts
│
├── docker-compose.yml             # локальний запуск усього: postgres + orchestrator + admin
├── Taskfile.yml                   # єдина точка керування проєктом (task up, task admin, ...)
├── .env.example                   # змінні оточення (спільні для docker-compose)
└── README.md
```

---

## 1. Швидкий старт (Docker)

Короткі команди описані в `Taskfile.yml` ([Task](https://taskfile.dev), встановити: `brew install go-task`).
Список усіх команд — просто `task`.

```bash
cp .env.example .env        # заповни токени та REPOS_DIR
task up                     # = docker compose up -d --build
curl localhost:8080/healthz # {"status":"ok"}
```

**Авторизація Claude Code в контейнері.** На Mac логін Claude зберігається в Keychain,
а контейнер до нього доступу не має, тому в `.env` потрібне одне з двох:

- `CLAUDE_CODE_OAUTH_TOKEN` — якщо є підписка Pro/Max: виконай на Mac `claude setup-token`;
- `ANTHROPIC_API_KEY` — ключ з console.anthropic.com.

**Jira.** Оркестратор сам у Jira не ходить: задачу читає і коментар пише Claude Code
через Jira MCP ([mcp-atlassian](https://github.com/sooperset/mcp-atlassian), встановлено в образі,
конфіг — `apps/orchestrator/mcp/jira.json`, роль `jira` у `config.yaml`). Потрібні `JIRA_URL` і
`JIRA_USERNAME` + `JIRA_API_TOKEN` (Jira Cloud) або `JIRA_PERSONAL_TOKEN` (Server/Data Center).

**Репозиторії.** `REPOS_DIR` у `.env` — папка на Mac, де лежать репозиторії
(наприклад `~/Projects`). Вона монтується в контейнер як `/workspaces`.
У `apps/orchestrator/config.yaml`:

- `main_repo` — репозиторій, у якому запускається claude (зараз `game-rgs-backend`);
- `exclude_repos` — для них worktree не створюється (зараз фронт: `game-client`, `game-configurator`).

Для **всіх інших** git-репозиторіїв з `REPOS_DIR` створюється worktree на гілці `feature/<KEY>`
від їхнього поточного HEAD. Список фіксується на старті задачі (таблиця `task_repos`).
Файли з `worktree_copy` (локальні конфіги, яких немає в git: `config/`, `k8s/local/config/`, `.env`)
копіюються з твоєї копії репозиторію в кожен worktree.

**Кластер для QA.** Перед тестуванням QA-агент запускає `test_env_script`
(`local-development/cluster/start-cluster.sh` з worktree задачі): він збирає образи з гілки задачі
і піднімає kind-кластер. Для цього в контейнері оркестратора працює власний Docker Engine
(тому контейнер `privileged`, образи кешуються у volume `docker_data`), а kind, kubectl і helm
встановлено в образі. Кластер один на всіх, тож QA різних задач чекають одне на одного.
Образи MinIO з docker.io анонімно не завантажуються — один раз передай їх з Mac:
`task images:minio`. Стан кластера — `task cluster`.
Docker Desktop має мати достатньо ресурсів для кластера (рекомендовано від 12 ГБ пам'яті).

## 2. Запуск задачі

Задачі запускаються **лише через адмін-панель**: `task admin` → http://localhost:3080.

- **«Нова задача»** — посилання на Jira або ключ (`PROJ-123`) і, за бажанням, основний репозиторій;
- **сторінка задачі** — статус, етапи, фідбек рев'юера, звіт QA, вартість і живий журнал агентів;
- **«Продовжити» / «Перезапустити»** — після перезапуску контейнера або для FAILED-задачі
  (звіт QA стає фідбеком для Developer'а).

Для діагностики з терміналу:

```bash
task watch KEY=PROJ-123              # живий журнал агентів (те саме, що в адмінці)
task changes KEY=PROJ-123            # коміти та незакомічені зміни в репозиторіях задачі
task logs                            # логи самого оркестратора
```

**Що робить агент.** Оркестратор запускає `claude --output-format stream-json` і кожну дію
агента (🔧 виклик інструмента, 💬 текст, ⚠️ помилка інструмента) одразу пише в лог і у файл
`/tmp/workspaces/.logs/<KEY>.log` — у ньому всі етапи задачі підряд. `task watch` показує цей файл.
Після кожного етапу той самий журнал разом з відповіддю агента зберігається в БД — його видно на сторінці задачі в адмінці.

Основний репозиторій можна змінити для окремої задачі: вибрати його в діалозі «Нова задача»
або поставити мітку `repo:billing-api` у Jira.

REST API, яким користується адмінка (задача виконується у фоні):

```bash
curl -X POST localhost:8080/api/tasks -d '{"jira_url":"PROJ-123"}'
curl localhost:8080/api/tasks
curl localhost:8080/api/tasks/PROJ-123
curl localhost:8080/api/tasks/PROJ-123/logs
curl -X POST localhost:8080/api/tasks/PROJ-123/resume
```

Результат роботи — гілка `feature/PROJ-123` у твоєму репозиторії:
`git log feature/PROJ-123`, далі push і Pull Request уже роблиш ти.

---

## 3. Як читати код

Код оркестратора лежить в `apps/orchestrator/`, шляхи нижче — відносно цієї папки.
Читай у такому порядку, від простого до головного:

| # | Файл | Що там |
|---|------|--------|
| 1 | `cmd/orchestrator/main.go` | Точка входу: читає `CONFIG_PATH` і запускає сервер |
| 2 | `cmd/orchestrator/app.go` | Збирає всі частини докупи, запускає HTTP-сервер |
| 3 | `internal/config/config.go` | Читання `config.yaml` + змінних оточення |
| 4 | `internal/storage/` | PostgreSQL: міграції (`migrations/*.sql`) і запити до таблиць |
| 5 | `internal/jira/` | Парсинг посилання, робота з Jira через Claude + Jira MCP |
| 6 | `internal/workspace/manager.go` | `git worktree add/remove`, коміти |
| 7 | `internal/agent/runner.go` | Запуск `claude -p ... --output-format json` |
| 8 | **`internal/pipeline/pipeline.go`** | **Головне: скінченний автомат, етапи, anti-loop** |
| 9 | `internal/pipeline/prompts.go` | Тексти завдань для агентів і розбір їхніх відповідей |
| 10 | `internal/api/server.go` | REST API |

Весь код прокоментований українською, включно з поясненнями, як працює сам Go.

### Мінімум Go, щоб читати цей код

| Конструкція | Що означає | Де подивитися |
|-------------|------------|---------------|
| `x, err := f()` + `if err != nil { return err }` | У Go немає винятків (exceptions): функція повертає помилку, і її перевіряють одразу | всюди |
| `fmt.Errorf("...: %w", err)` | Додати контекст до помилки, не загубивши оригінал | `storage/tasks.go` |
| `type Task struct { ... }` | Структура (приблизно як клас без спадкування) | `storage/tasks.go` |
| `` `db:"id" json:"id"` `` | Теги: підказки бібліотекам, як мапити поле в SQL / JSON | `storage/tasks.go` |
| `func (p *Pipeline) Run(...)` | Метод типу. `*` означає вказівник: метод може змінювати `p` | `pipeline/pipeline.go` |
| `interface { GetIssue(...) }` | Інтерфейс; тип реалізує його автоматично, якщо має ці методи | `jira/jira.go` |
| `defer x.Close()` | Виконати в кінці функції (як `finally`) | `agent/runner.go` |
| `ctx context.Context` | Скасування і таймаути: Ctrl+C зупиняє все акуратно | `main.go`, `agent/runner.go` |
| `go func() { ... }()` / `wg.Go(...)` | Запустити код паралельно в goroutine (легкий потік) | `api/server.go`, `app.go` |
| `chan`, `select` | Канали: спосіб, яким goroutine передають дані одна одній | `app.go` (`Serve`) |
| `switch { case ...: }` | switch без змінної = ланцюжок if/else if | `pipeline/pipeline.go` |
| `internal/` | Пакети, які не можна імпортувати ззовні проєкту | структура папок |
| `*_test.go`, `func TestXxx(t *testing.T)` | Тести. Запуск: `go test ./...` | `jira/jira_test.go` |

### Технології

- **Go 1.27**, лише стандартна бібліотека для HTTP (`net/http` з роутингом `GET /api/tasks/{id}`),
  логування (`log/slog`) і процесів (`os/exec`);
- **pgx v5** — драйвер PostgreSQL, без ORM, звичайний SQL;
- **goccy/go-yaml** — читання YAML;
- **PostgreSQL 18**, **Node 24 + Debian 13 (trixie)**, **Claude Code CLI**, **Playwright MCP + Chromium**.

---

## 4. Локальна розробка

Go ставити не обов'язково: `task test` запускає тести в Docker. Якщо хочеш локально:

```bash
brew install go
cd apps/orchestrator
go test ./...
go run ./cmd/orchestrator   # REST API на :8080, потрібен запущений postgres (task up)
```

Корисні команди Go: `go build ./...` (зібрати), `go vet ./...` (статичний аналіз),
`gofmt -w .` (форматування — в Go воно єдине для всіх, сперечатися не треба).

Адмінка з hot reload: `task admin:dev` (або `cd apps/admin && npm install && npm run dev`) → http://localhost:5173.

---

## 5. Що варто знати

- **Агенти працюють з `--dangerously-skip-permissions`** — тобто без підтверджень
  можуть виконувати будь-які команди всередині контейнера. Саме тому вони запускаються в Docker.
  Під root Claude Code такий режим забороняє, тому контейнер працює від користувача `node`.
- **Git worktree створюється всередині контейнера** (`/tmp/workspaces`, окремий volume).
  Якщо на Mac виконати `git worktree list`, такі worktree будуть позначені як `prunable`
  (шлях існує лише в контейнері). **Не запускай `git worktree prune` на Mac**, поки задача виконується.
- `~/.claude` з Mac змонтовано в контейнер: агенти бачать твої глобальні CLAUDE.md, skills і agents.
- Правила конкретного репозиторію (CLAUDE.md, `.mcp.json`, `.claude/`) Claude Code підхоплює сам,
  бо запускається всередині worktree цього репозиторію.
- Browser MCP для тестувальника підключається через `apps/orchestrator/mcp/browser.json`
  (`--mcp-config` у `config.yaml`, роль `tester`).
- Jira MCP підключається лише для ролі `jira` (`apps/orchestrator/mcp/jira.json` + `--strict-mcp-config`),
  дозволені тільки інструменти `jira_get_issue` і `jira_add_comment` (`ENABLED_TOOLS`).
  Агенти Developer/Reviewer/QA доступу до Jira не мають.

### Відмінності від специфікації (`project.md`)

| Що | Чому |
|----|------|
| Додані колонки `title`, `description`, `base_commit`, `last_error` | Щоб `resume` працював без повторного запиту в Jira, а рев'юер бачив diff від правильного коміту |
| Маркери `VERDICT: APPROVE` / `TEST_RESULT: PASSED` в останньому рядку | Слово "APPROVE" може трапитися всередині тексту ("не можу APPROVE") — маркер надійніший. Просто `APPROVE` останнім рядком теж зараховується |
| Оркестратор сам комітить зміни після кожного етапу | Інакше при видаленні worktree код би зник |
| Кнопка «Продовжити» (`resume`) | Продовжити після перезапуску контейнера або перезапустити FAILED-задачу (звіт QA стає фідбеком для Developer'а) |
| Прибрано `network_mode: "bridge"` з compose | З ним контейнер не бачить сервіс `postgres` за іменем |
| Postgres 18, Go 1.27, Node 24, Debian trixie | Актуальні версії; для Postgres 18 volume монтується в `/var/lib/postgresql` |
| Немає CLI — лише REST API + адмін-панель | Один спосіб запуску задач: pipeline виконується тільки в сервері, стан видно в UI |
