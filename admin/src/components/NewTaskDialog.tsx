import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { Play, X } from 'lucide-react'
import { api, type Info } from '../api'

export function NewTaskDialog({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate()
  const [jira, setJira] = useState('')
  const [repo, setRepo] = useState('')
  const [info, setInfo] = useState<Info | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.info().then(setInfo).catch(() => setInfo(null))
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      // Читання задачі з Jira через MCP займає 10–30 секунд.
      const task = await api.create(jira.trim(), repo)
      navigate(`/tasks/${task.id}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <form className="dialog" onSubmit={submit}>
        <div className="dialog-head">
          <div>
            <h2>Нова задача</h2>
            <p>Агенти візьмуть її з Jira і проведуть через розробку, рев'ю та тести.</p>
          </div>
          <button type="button" className="btn btn-ghost btn-icon" onClick={onClose} disabled={busy} aria-label="Закрити">
            <X size={18} />
          </button>
        </div>

        <div className="field">
          <label htmlFor="jira">Задача Jira</label>
          <input
            id="jira"
            className="input"
            placeholder="CP-509 або посилання на задачу"
            value={jira}
            onChange={(e) => setJira(e.target.value)}
            autoFocus
            required
          />
        </div>

        <div className="field">
          <label htmlFor="repo">Основний репозиторій</label>
          <select id="repo" className="input" value={repo} onChange={(e) => setRepo(e.target.value)}>
            <option value="">
              {info ? `За замовчуванням — ${info.main_repo}` : 'За замовчуванням'}
            </option>
            {info?.repos.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
          <span className="hint">
            Тут запуститься Claude. Worktree створюються для всіх {info ? info.repos.length : ''} репозиторіїв
            {info?.exclude_repos.length ? `, крім ${info.exclude_repos.join(', ')}` : ''}.
          </span>
        </div>

        {error && <div className="error-bar">{error}</div>}

        <div className="dialog-foot">
          <button type="button" className="btn btn-soft" onClick={onClose} disabled={busy}>Скасувати</button>
          <button type="submit" className="btn btn-primary" disabled={busy || !jira.trim()}>
            <Play size={16} /> {busy ? 'Читаю задачу з Jira…' : 'Запустити'}
          </button>
        </div>
      </form>
    </div>
  )
}
