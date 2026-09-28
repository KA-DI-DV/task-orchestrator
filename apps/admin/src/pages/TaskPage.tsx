import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import {
  Activity, ArrowLeft, Check, ChevronRight, Clock, Coins, Copy, ExternalLink, FileText, FlaskConical,
  Footprints, FolderGit2, GitBranch, GitCommitHorizontal, History, MessageSquareQuote, RefreshCw,
  RotateCcw, Timer, TriangleAlert,
} from 'lucide-react'
import { api, isFinal, type Task, type TaskLog, type TaskRepo } from '../api'
import { usePolling } from '../hooks'
import { dateTime, duration, money, plural, shortSha, timeAgo } from '../format'
import { ROLE, VERDICT } from '../meta'
import { RoleIcon, StatusBadge, VerdictBadge } from '../components/Badges'
import { PipelineSteps } from '../components/Pipeline'
import { Clamp } from '../components/Markdown'

type Tab = 'overview' | 'timeline' | 'repos' | 'live'

export function TaskPage() {
  const { id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) || 'overview'
  const setTab = (t: Tab) => setParams(t === 'overview' ? {} : { tab: t }, { replace: true })

  const task = usePolling(`task-${id}`, () => api.task(id), 4000)
  const running = task.data ? !isFinal(task.data.status) : true
  const logs = usePolling(`logs-${id}`, () => api.logs(id), 6000, running)

  if (task.error && !task.data) {
    return (
      <div className="page">
        <Link to="/" className="back"><ArrowLeft size={16} /> Усі задачі</Link>
        <div className="error-bar">Не вдалося завантажити задачу {id}: {task.error}</div>
      </div>
    )
  }
  if (!task.data) {
    return (
      <div className="page">
        <div className="skeleton" style={{ height: 120 }} />
        <div className="skeleton" style={{ height: 96 }} />
        <div className="skeleton" style={{ height: 320 }} />
      </div>
    )
  }

  const t = task.data
  const history = logs.data ?? []
  const changed = (t.repos ?? []).filter((r) => r.commits > 0)

  return (
    <div className="page">
      <Link to="/" className="back"><ArrowLeft size={16} /> Усі задачі</Link>

      <Header task={t} onResumed={() => { void task.refresh(); void logs.refresh() }} />
      <PipelineSteps status={t.status} logs={history} />

      <div className="tabs" role="tablist">
        <TabButton active={tab === 'overview'} onClick={() => setTab('overview')} icon={<FileText size={16} />}>Огляд</TabButton>
        <TabButton active={tab === 'timeline'} onClick={() => setTab('timeline')} icon={<History size={16} />} count={history.length}>Хронологія</TabButton>
        <TabButton active={tab === 'repos'} onClick={() => setTab('repos')} icon={<FolderGit2 size={16} />} count={changed.length}>Репозиторії</TabButton>
        <TabButton active={tab === 'live'} onClick={() => setTab('live')} icon={<Activity size={16} />}>Живий журнал</TabButton>
      </div>

      {tab === 'overview' && <Overview task={t} logs={history} />}
      {tab === 'timeline' && <Timeline logs={history} loading={logs.data === null} />}
      {tab === 'repos' && <Repos repos={t.repos ?? []} branch={t.branch_name} />}
      {tab === 'live' && <Live id={t.id} running={running} />}
    </div>
  )
}

function TabButton({ active, onClick, icon, count, children }: {
  active: boolean; onClick: () => void; icon: React.ReactNode; count?: number; children: React.ReactNode
}) {
  return (
    <button role="tab" aria-selected={active} className={`tab ${active ? 'active' : ''}`} onClick={onClick}>
      {icon} {children} {count ? <span className="count">{count}</span> : null}
    </button>
  )
}

// ───────────────────────────── Шапка ─────────────────────────────

function Header({ task: t, onResumed }: { task: Task; onResumed: () => void }) {
  const [copied, setCopied] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function copyBranch() {
    try {
      await navigator.clipboard.writeText(t.branch_name)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // буфер обміну недоступний (http без localhost) — нічого страшного
    }
  }

  async function resume() {
    // Незавершену задачу продовжуємо лише після підтвердження: якщо її ще виконує
    // інший процес (task run у терміналі), другий запуск їй зашкодить.
    if (!isFinal(t.status) && !confirm) {
      setConfirm(true)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await api.resume(t.id)
      setConfirm(false)
      onResumed()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const jiraLink = /^https?:\/\//.test(t.jira_url) ? t.jira_url : null

  return (
    <div className="task-head">
      <div className="task-head-main">
        <div className="task-head-line">
          <span className="key">{t.id}</span>
          <StatusBadge status={t.status} large />
          {jiraLink && (
            <a className="btn btn-ghost btn-sm" href={jiraLink} target="_blank" rel="noreferrer">
              <ExternalLink size={14} /> Jira
            </a>
          )}
        </div>
        <h1>{t.title || 'Без назви'}</h1>
        <div className="facts">
          <span>
            <GitBranch size={15} />
            <button className="copy mono" onClick={copyBranch} title="Скопіювати назву гілки">
              {t.branch_name} {copied ? <Check size={14} /> : <Copy size={14} />}
            </button>
          </span>
          <span><FolderGit2 size={15} /> основний: <b>{t.repo_name}</b></span>
          <span><Clock size={15} /> створено {dateTime(t.created_at)}</span>
          <span><Coins size={15} /> <b>{money(t.total_cost_usd)}</b></span>
        </div>
        {error && <div className="error-bar">{error}</div>}
      </div>

      {t.status !== 'COMPLETED' && (
        <div className="stack" style={{ alignItems: 'flex-end', gap: 8 }}>
          <button className={`btn ${t.status === 'FAILED' ? 'btn-primary' : 'btn-soft'}`} onClick={resume} disabled={busy}>
            {busy ? <RefreshCw size={16} className="spin" /> : <RotateCcw size={16} />}
            {t.status === 'FAILED' ? 'Перезапустити' : confirm ? 'Так, продовжити' : 'Продовжити'}
          </button>
          {confirm && (
            <span className="muted" style={{ fontSize: 12.5, maxWidth: 260, textAlign: 'right' }}>
              Продовжуй, лише якщо задача зараз не виконується (наприклад, контейнер перезапускали).
            </span>
          )}
        </div>
      )}
    </div>
  )
}

// ───────────────────────────── Огляд ─────────────────────────────

function Overview({ task: t, logs }: { task: Task; logs: TaskLog[] }) {
  const runs = logs.filter((l) => l.agent_role !== 'orchestrator')
  const agentMs = runs.reduce((s, l) => s + l.duration_ms, 0)
  const turns = runs.reduce((s, l) => s + l.num_turns, 0)
  const reviews = runs.filter((l) => l.agent_role === 'reviewer').length
  const forced = t.review_attempts >= t.max_review_attempts

  return (
    <div className="grid-2">
      <div className="stack">
        {t.last_error && (
          <div className="callout tone-red">
            <TriangleAlert size={18} />
            <div className="callout-body">
              <div className="callout-title">Задача завершилась помилкою</div>
              <div className="mono" style={{ whiteSpace: 'pre-wrap' }}>{t.last_error}</div>
            </div>
          </div>
        )}
        {forced && (
          <div className="callout tone-amber">
            <TriangleAlert size={18} />
            <div className="callout-body">
              <div className="callout-title">Рев'ю не схвалено за {t.max_review_attempts} {plural(t.max_review_attempts, 'спробу', 'спроби', 'спроб')}</div>
              Задачу форсовано передано на тестування — потрібна ручна перевірка коду.
            </div>
          </div>
        )}

        <div className="card card-pad">
          <div className="card-title"><FileText size={18} /><h3>Опис задачі</h3></div>
          {t.description ? <Clamp text={t.description} limit={1200} /> : <span className="muted">Опису в Jira немає.</span>}
        </div>

        {t.last_feedback && (
          <div className="card card-pad">
            <div className="card-title"><MessageSquareQuote size={18} /><h3>Останні зауваження</h3></div>
            <Clamp text={t.last_feedback} />
          </div>
        )}

        {t.test_report && (
          <div className="card card-pad">
            <div className="card-title"><FlaskConical size={18} /><h3>Звіт QA</h3></div>
            <Clamp text={t.test_report} />
          </div>
        )}
      </div>

      <div className="stack">
        <div className="card card-pad">
          <div className="card-title"><h3>Підсумок</h3></div>
          <dl className="kv">
            <dt>Рев'ю</dt><dd>{reviews} {plural(reviews, 'раунд', 'раунди', 'раундів')} · {t.review_attempts}/{t.max_review_attempts} повернень</dd>
            <dt>Запусків агентів</dt><dd>{runs.length}</dd>
            <dt>Кроків агентів</dt><dd>{turns}</dd>
            <dt>Час роботи агентів</dt><dd>{agentMs ? duration(agentMs) : '—'}</dd>
            <dt>Вартість</dt><dd>{money(t.total_cost_usd)}</dd>
            <dt>Змінено репозиторіїв</dt><dd>{t.changed_repos} з {(t.repos ?? []).length}</dd>
            <dt>Оновлено</dt><dd>{timeAgo(t.updated_at)}</dd>
            {t.completed_at && (<><dt>Завершено</dt><dd>{dateTime(t.completed_at)}</dd></>)}
          </dl>
        </div>
      </div>
    </div>
  )
}

// ───────────────────────────── Хронологія ─────────────────────────────

function Timeline({ logs, loading }: { logs: TaskLog[]; loading: boolean }) {
  // Групуємо запуски за ітерацією Developer ↔ Reviewer.
  const groups = useMemo(() => {
    const map = new Map<number, TaskLog[]>()
    for (const l of logs) {
      const key = l.iteration || 1
      map.set(key, [...(map.get(key) ?? []), l])
    }
    return [...map.entries()].sort((a, b) => a[0] - b[0])
  }, [logs])

  if (loading) return <div className="skeleton" style={{ height: 240 }} />
  if (logs.length === 0) {
    return (
      <div className="card">
        <div className="empty">
          <div className="empty-art"><History size={32} /></div>
          <h3>Історії ще немає</h3>
          <p>Запис з’явиться, щойно перший агент завершить свій етап. Що він робить зараз — у вкладці «Живий журнал».</p>
        </div>
      </div>
    )
  }

  return (
    <div className="timeline">
      {groups.map(([iteration, runs]) => (
        <section key={iteration}>
          <div className="iteration-head">
            <h2>Ітерація {iteration}</h2>
            <div className="line" />
          </div>
          <div className="runs">
            {runs.map((l) => <Run key={l.id} log={l} />)}
          </div>
        </section>
      ))}
    </div>
  )
}

function Run({ log: l }: { log: TaskLog }) {
  const role = ROLE[l.agent_role] ?? ROLE.orchestrator
  const tone = VERDICT[l.verdict]?.tone ?? role.tone
  const isError = l.agent_role === 'orchestrator'

  return (
    <div className="run">
      <span className={`run-node tone-${tone}`} />
      <div className="card">
        <div className="run-head">
          <div className="run-role">
            <RoleIcon role={l.agent_role} />
            {role.label}
          </div>
          <VerdictBadge verdict={l.verdict} />
          <div className="run-meta">
            <span><Clock size={14} /> {dateTime(l.created_at)}</span>
            {l.duration_ms > 0 && <span><Timer size={14} /> {duration(l.duration_ms)}</span>}
            {l.num_turns > 0 && <span><Footprints size={14} /> {l.num_turns} {plural(l.num_turns, 'крок', 'кроки', 'кроків')}</span>}
            {l.cost_usd > 0 && <span><Coins size={14} /> {money(l.cost_usd)}</span>}
          </div>
        </div>

        <div className="run-body">
          {isError ? (
            <div className="callout tone-red">
              <TriangleAlert size={18} />
              <div className="callout-body mono" style={{ whiteSpace: 'pre-wrap' }}>{l.summary}</div>
            </div>
          ) : (
            l.summary && <Clamp text={l.summary} />
          )}

          {l.feedback && (
            <div className="file-card">
              <div className="file-card-head"><FileText size={14} /> review_feedback.md</div>
              <div className="file-card-body"><Clamp text={l.feedback} limit={1200} /></div>
            </div>
          )}

          {l.prompt && <Fold title="Завдання, яке отримав агент" text={l.prompt} />}
          {l.output_log && <Fold title="Журнал дій агента" text={l.output_log} />}
        </div>
      </div>
    </div>
  )
}

function Fold({ title, text }: { title: string; text: string }) {
  return (
    <details className="fold">
      <summary><ChevronRight size={16} className="chev" /> {title}</summary>
      <div className="fold-body"><pre className="code">{text}</pre></div>
    </details>
  )
}

// ───────────────────────────── Репозиторії ─────────────────────────────

function Repos({ repos, branch }: { repos: TaskRepo[]; branch: string }) {
  const changed = repos.filter((r) => r.commits > 0)
  const quiet = repos.filter((r) => r.commits === 0)

  return (
    <div className="stack">
      {changed.length === 0 ? (
        <div className="card">
          <div className="empty">
            <div className="empty-art"><FolderGit2 size={32} /></div>
            <h3>Змін ще немає</h3>
            <p>Коміти з’являються після кожного етапу агента. Гілка задачі — <span className="mono">{branch}</span>.</p>
          </div>
        </div>
      ) : (
        <div className="repo-grid">
          {changed.map((r) => <RepoCard key={r.repo_name} repo={r} />)}
        </div>
      )}

      {quiet.length > 0 && (
        <div className="card">
          <div className="card-pad" style={{ paddingBottom: 0 }}>
            <div className="card-title" style={{ marginBottom: 0 }}>
              <h3>Без змін</h3>
              <span className="muted" style={{ fontSize: 13 }}>
                worktree створено, але задача їх не зачепила — порожні гілки видаляються після завершення
              </span>
            </div>
          </div>
          <div className="repo-quiet">
            {quiet.map((r) => (
              <span key={r.repo_name} className="repo-pill">
                <FolderGit2 size={14} /> {r.repo_name}{r.is_main ? ' · основний' : ''}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function RepoCard({ repo: r }: { repo: TaskRepo }) {
  const lines = r.diff_stat.split('\n')
  const summary = lines.at(-1) ?? ''
  const files = lines.slice(0, -1)
  return (
    <div className="card repo">
      <div className="repo-head">
        <span className="repo-icon"><FolderGit2 size={18} /></span>
        <div style={{ minWidth: 0 }}>
          <div className="repo-name">{r.repo_name}</div>
          <div className="repo-sub mono">
            <span><GitCommitHorizontal size={12} /> {shortSha(r.base_commit)} → {shortSha(r.head_commit)}</span>
          </div>
        </div>
        <span className="badge tone-green">{r.commits} {plural(r.commits, 'коміт', 'коміти', 'комітів')}</span>
      </div>
      {r.is_main && <div className="muted" style={{ padding: '0 16px 10px', fontSize: 12.5 }}>Основний репозиторій — тут працював Claude</div>}
      {r.diff_stat && (
        <pre className="diffstat mono">
          {files.map((line, i) => <DiffLine key={i} line={line} />)}
          <div style={{ marginTop: 6, fontWeight: 700 }}>{summary.trim()}</div>
        </pre>
      )}
    </div>
  )
}

// Рядок git diff --stat: " src/a.ts | 12 ++++--" — плюси зеленим, мінуси червоним.
function DiffLine({ line }: { line: string }) {
  const m = line.match(/^(.*\|\s*\d+\s)(\+*)(-*)\s*$/)
  if (!m) return <div>{line}</div>
  return <div>{m[1]}<span className="add">{m[2]}</span><span className="del">{m[3]}</span></div>
}

// ───────────────────────────── Живий журнал ─────────────────────────────

function Live({ id, running }: { id: string; running: boolean }) {
  const { data, error } = usePolling(`activity-${id}`, () => api.activity(id), 2500, running)
  const box = useRef<HTMLDivElement>(null)
  const stick = useRef(true) // тримаємося низу, поки користувач не прокрутив угору

  useLayoutEffect(() => {
    const el = box.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [data])

  useEffect(() => {
    const el = box.current
    if (!el) return
    const onScroll = () => { stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40 }
    el.addEventListener('scroll', onScroll)
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  const lines = data ?? []

  return (
    <div className="card" style={{ overflow: 'hidden' }}>
      <div className="live-head">
        <div className="card-title" style={{ margin: 0 }}>
          <Activity size={18} />
          <h3>Що роблять агенти</h3>
        </div>
        {running ? (
          <span className="badge tone-green"><span className="pulse" /> наживо</span>
        ) : (
          <span className="badge tone-neutral">задача завершена</span>
        )}
      </div>
      {error && <div className="error-bar" style={{ margin: 12 }}>{error}</div>}
      <div className="activity" ref={box}>
        {lines.length === 0 ? (
          <div className="empty" style={{ padding: 40 }}>
            <p>Агенти ще нічого не зробили. Журнал з’явиться, щойно стартує перший етап.</p>
          </div>
        ) : (
          lines.map((line, i) => <ActivityLine key={i} line={line} />)
        )}
      </div>
    </div>
  )
}

// Рядок журналу: "14:05:01 [developer] 🔧 Bash: go test ./..."
function ActivityLine({ line }: { line: string }) {
  const m = line.match(/^(\d\d:\d\d:\d\d) \[([^\]]+)\] (.*)$/)
  if (!m) return <div className="act-line"><span /><span /><span className="act-text">{line}</span></div>
  const [, time, role, text] = m
  const kind = text.startsWith('🔧') ? 'tool'
    : text.startsWith('💬') ? 'say'
    : text.startsWith('⚠️') || text.startsWith('⏱') ? 'warn'
    : text.startsWith('▶') || text.startsWith('■') ? 'edge'
    : ''
  const meta = ROLE[role]
  return (
    <div className={`act-line ${kind}`}>
      <span className="act-time">{time}</span>
      <span className={`act-role tone-${meta?.tone ?? 'neutral'}`}>{meta?.label ?? role}</span>
      <span className="act-text">{text}</span>
    </div>
  )
}
