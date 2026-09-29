import { useEffect, useState } from 'react'
import { Check, History, RefreshCw, RotateCcw, Sparkles } from 'lucide-react'
import { api, type RoleSetting } from '../api'
import { ROLE } from '../meta'
import { RoleIcon } from '../components/Badges'

// Що дає продовження сесії кожній ролі — коли задача до неї повертається.
const RESUME_HINT: Record<string, string> = {
  architect: 'Переробляючи план після твоїх правок, пам’ятає, що вже дослідив у коді, і свої попередні версії плану.',
  developer: 'На наступній ітерації після рев’ю або QA пам’ятає, як і чому писав код, і не досліджує його наново.',
  reviewer: 'На повторному рев’ю пам’ятає свої зауваження і перевіряє, чи їх виправлено. Але «свіже око» може помітити більше.',
  tester: 'Після повторного тестування пам’ятає, що вже перевіряв і що падало минулого разу.',
}

export function SettingsPage() {
  const [roles, setRoles] = useState<RoleSetting[] | null>(null)
  const [saving, setSaving] = useState<string | null>(null)
  const [saved, setSaved] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.settings().then(setRoles).catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  async function toggle(role: string, resume: boolean) {
    if (!roles) return
    setSaving(role)
    setError(null)
    try {
      setRoles(await api.saveSettings(roles.map((r) => (r.role === role ? { ...r, resume_session: resume } : r))))
      setSaved(role)
      setTimeout(() => setSaved((s) => (s === role ? null : s)), 1500)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(null)
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Налаштування</h1>
          <p>Однакові для всіх задач і діють з наступного запуску агента.</p>
        </div>
      </div>

      {error && <div className="error-bar">{error}</div>}

      <div className="card card-pad">
        <div className="card-title"><History size={18} /><h3>Сесії агентів</h3></div>
        <p className="muted" style={{ marginTop: 0 }}>
          Задача ходить між ролями: розробник → рев’ю → знову розробник… Коли вона повертається до ролі, агент може
          <b> продовжити свою сесію</b> (<span className="mono">claude --resume</span>) і пам’ятати всю попередню
          роботу над задачею, або <b>почати заново</b> — з чистою пам’яттю, знаючи лише завдання, код і зауваження.
          Продовження дає більше контексту, але кожне повернення робить розмову довшою й дорожчою.
        </p>

        {roles === null && !error ? (
          <div className="skeleton" style={{ height: 240 }} />
        ) : (
          <div className="settings-list">
            {(roles ?? []).map((r) => (
              <div key={r.role} className="settings-row">
                <RoleIcon role={r.role} />
                <div className="settings-text">
                  <div className="settings-name">
                    {ROLE[r.role]?.label ?? r.role}
                    {saving === r.role && <RefreshCw size={14} className="spin muted" />}
                    {saved === r.role && <span className="badge tone-green"><Check size={12} /> збережено</span>}
                  </div>
                  <div className="muted settings-hint">{RESUME_HINT[r.role]}</div>
                </div>
                <div className="chips" role="radiogroup" aria-label={`Сесія: ${ROLE[r.role]?.label ?? r.role}`}>
                  <button role="radio" aria-checked={r.resume_session} className={`chip ${r.resume_session ? 'active' : ''}`}
                    onClick={() => !r.resume_session && toggle(r.role, true)} disabled={saving !== null}>
                    <RotateCcw size={14} /> Продовжує сесію
                  </button>
                  <button role="radio" aria-checked={!r.resume_session} className={`chip ${!r.resume_session ? 'active' : ''}`}
                    onClick={() => r.resume_session && toggle(r.role, false)} disabled={saving !== null}>
                    <Sparkles size={14} /> Починає заново
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        <p className="muted" style={{ fontSize: 13, marginBottom: 0 }}>
          Незалежно від цього налаштування зупинена задача після «Продовжити» завжди відновлює ту саму сесію,
          на якій її зупинили.
        </p>
      </div>
    </div>
  )
}
