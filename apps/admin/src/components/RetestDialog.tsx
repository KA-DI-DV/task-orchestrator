import { useEffect, useState, type FormEvent } from 'react'
import { FlaskConical, X } from 'lucide-react'
import { api, type Task } from '../api'

// Повторне тестування готової / впалої задачі: запускається лише QA-агент,
// за бажання — з додатковими інструкціями.
export function RetestDialog({ task: t, onClose, onStarted }: { task: Task; onClose: () => void; onStarted: () => void }) {
  const [instructions, setInstructions] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !busy && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api.retest(t.id, instructions.trim())
      onStarted()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <form className="dialog" onSubmit={submit} style={{ width: 'min(560px, 100%)' }}>
        <div className="dialog-head">
          <div>
            <h2>Перетестувати {t.id}</h2>
            <p>Запуститься лише QA-агент на гілці <span className="mono">{t.branch_name}</span>. Розробка і рев’ю не повторюються.</p>
          </div>
          <button type="button" className="btn btn-ghost btn-icon" onClick={onClose} disabled={busy} aria-label="Закрити">
            <X size={18} />
          </button>
        </div>

        <div className="field">
          <label htmlFor="qa-instructions">Додаткові інструкції для QA</label>
          <textarea
            id="qa-instructions"
            className="input textarea"
            placeholder="Необов’язково. Наприклад: перевір лише ручний перезапуск виплат у back-office; прожени rgs-system-tests smoke; не піднімай UI."
            value={instructions}
            onChange={(e) => setInstructions(e.target.value)}
            disabled={busy}
            autoFocus
          />
          <span className="hint">
            Інструкції мають пріоритет над звичайним завданням QA. Без них агент перевірить задачу повністю, як зазвичай.
            Якщо тести не пройдуть, задача стане «Впала», і «Перезапустити» передасть звіт QA розробнику.
          </span>
        </div>

        {error && <div className="error-bar">{error}</div>}

        <div className="dialog-foot">
          <button type="button" className="btn btn-soft" onClick={onClose} disabled={busy}>Скасувати</button>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            <FlaskConical size={16} /> {busy ? 'Запускаю…' : 'Запустити тестування'}
          </button>
        </div>
      </form>
    </div>
  )
}
