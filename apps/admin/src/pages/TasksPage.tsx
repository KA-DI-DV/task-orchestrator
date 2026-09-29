import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import {
  CircleCheck, CircleX, Coins, Eye, FolderGit2, GitBranch, Hourglass, Inbox, Plus, Search, Sparkles,
} from 'lucide-react'
import { api, isFinal, isStopped, isWaiting, type Task } from '../api'
import { usePolling } from '../hooks'
import { COST_HINT, money, plural, timeAgo } from '../format'
import { StatusBadge } from '../components/Badges'
import { PipelineDots } from '../components/Pipeline'
import { NewTaskDialog } from '../components/NewTaskDialog'

type Filter = 'all' | 'waiting' | 'active' | 'done' | 'failed'

const FILTERS: { key: Filter; label: string; match: (t: Task) => boolean }[] = [
  { key: 'all', label: 'Усі', match: () => true },
  { key: 'waiting', label: 'Чекають на тебе', match: (t) => isWaiting(t.status) },
  { key: 'active', label: 'В роботі', match: (t) => !isFinal(t.status) && !isWaiting(t.status) },
  { key: 'done', label: 'Готові', match: (t) => t.status === 'COMPLETED' },
  { key: 'failed', label: 'Впали', match: (t) => t.status === 'FAILED' },
]

function greeting(): string {
  const h = new Date().getHours()
  if (h < 6) return 'Доброї ночі'
  if (h < 12) return 'Доброго ранку'
  if (h < 18) return 'Доброго дня'
  return 'Доброго вечора'
}

export function TasksPage() {
  const { data: tasks, error } = usePolling('tasks', api.tasks, 5000)
  const [filter, setFilter] = useState<Filter>('all')
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState(false)

  const list = tasks ?? []
  const active = list.filter((t) => !isFinal(t.status) && !isWaiting(t.status)).length
  const waiting = list.filter((t) => isWaiting(t.status)).length
  const done = list.filter((t) => t.status === 'COMPLETED').length
  const failed = list.filter((t) => t.status === 'FAILED').length
  const cost = list.reduce((sum, t) => sum + t.total_cost_usd, 0)

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    const match = FILTERS.find((f) => f.key === filter)!.match
    return list.filter((t) => match(t) && (!q || t.id.toLowerCase().includes(q) || t.title.toLowerCase().includes(q)))
  }, [list, filter, query])

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>{greeting()} ☕</h1>
          <p>
            {active > 0
              ? `Агенти зараз працюють над ${active} ${plural(active, 'задачею', 'задачами', 'задачами')}.`
              : 'Зараз агенти відпочивають — саме час дати їм нову задачу.'}
            {waiting > 0 && ` ${waiting} ${plural(waiting, 'план чекає', 'плани чекають', 'планів чекають')} на твоє рішення.`}
          </p>
        </div>
        <button className="btn btn-primary" onClick={() => setCreating(true)}>
          <Plus size={18} /> Нова задача
        </button>
      </div>

      <div className="stats">
        <Stat tone="blue" icon={<Hourglass size={20} />} value={active} label="В роботі" />
        <Stat tone="green" icon={<CircleCheck size={20} />} value={done} label="Готові" />
        <Stat tone="red" icon={<CircleX size={20} />} value={failed} label="Впали" />
        <Stat tone="accent" icon={<Coins size={20} />} value={money(cost)} label="Умовна вартість (за цінами API)" title={COST_HINT} />
      </div>

      <div className="toolbar">
        <div className="chips">
          {FILTERS.map((f) => (
            <button key={f.key} className={`chip ${filter === f.key ? 'active' : ''}`} onClick={() => setFilter(f.key)}>
              {f.label} <span className="count">{list.filter(f.match).length}</span>
            </button>
          ))}
        </div>
        <label className="search">
          <Search size={16} />
          <input placeholder="Пошук за ключем або назвою" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
      </div>

      {error && <div className="error-bar">Не вдалося завантажити задачі: {error}</div>}

      {tasks === null && !error ? (
        <div className="task-list">
          {[0, 1, 2].map((i) => <div key={i} className="skeleton" style={{ height: 84 }} />)}
        </div>
      ) : visible.length === 0 ? (
        <div className="card">
          <div className="empty">
            <div className="empty-art">{list.length === 0 ? <Sparkles size={32} /> : <Inbox size={32} />}</div>
            <h3>{list.length === 0 ? 'Поки що тут тихо' : 'Нічого не знайдено'}</h3>
            <p>
              {list.length === 0
                ? 'Запусти першу задачу — і тут з’явиться її шлях від розробки до тестів.'
                : 'Спробуй інший фільтр або пошуковий запит.'}
            </p>
            {list.length === 0 && (
              <button className="btn btn-primary" onClick={() => setCreating(true)} style={{ marginTop: 8 }}>
                <Plus size={18} /> Нова задача
              </button>
            )}
          </div>
        </div>
      ) : (
        <div className="task-list">
          {visible.map((t) => <TaskRow key={t.id} task={t} />)}
        </div>
      )}

      {creating && <NewTaskDialog onClose={() => setCreating(false)} />}
    </div>
  )
}

function Stat({ tone, icon, value, label, title }: { tone: string; icon: React.ReactNode; value: React.ReactNode; label: string; title?: string }) {
  return (
    <div className="card stat" title={title}>
      <div className={`stat-icon tone-${tone}`}>{icon}</div>
      <div>
        <div className="stat-value">{value}</div>
        <div className="stat-label">{label}</div>
      </div>
    </div>
  )
}

function TaskRow({ task: t }: { task: Task }) {
  return (
    <Link to={`/tasks/${t.id}`} className="card task-row">
      <div className="task-main">
        <div className="task-top">
          <span className="key">{t.id}</span>
          <span className="task-title">{t.title || 'Без назви'}</span>
        </div>
        <div className="task-meta">
          <span><GitBranch size={14} /> {t.repo_name}</span>
          <span>
            <FolderGit2 size={14} />
            {t.changed_repos > 0
              ? `змінено ${t.changed_repos} ${plural(t.changed_repos, 'репозиторій', 'репозиторії', 'репозиторіїв')}`
              : 'змін ще немає'}
          </span>
          <span><Eye size={14} /> рев'ю {t.review_attempts}/{t.max_review_attempts}</span>
          <span title={COST_HINT}><Coins size={14} /> {money(t.total_cost_usd)}</span>
          <span>оновлено {timeAgo(t.updated_at)}</span>
        </div>
      </div>
      <div className="task-side">
        <PipelineDots status={t.status} />
        <StatusBadge status={t.status} stopped={isStopped(t)} />
      </div>
    </Link>
  )
}
