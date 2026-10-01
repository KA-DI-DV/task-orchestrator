import { useEffect, useState } from 'react'
import { Check, Cpu, History, RefreshCw, RotateCcw, Sparkles, TriangleAlert } from 'lucide-react'
import { api, type RoleSetting } from '../api'
import { MODELS, ROLE } from '../meta'
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
  const [models, setModels] = useState<string[]>([])
  const [saving, setSaving] = useState<string | null>(null)
  const [saved, setSaved] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.settings()
      .then((s) => { setRoles(s.roles); setModels(s.models) })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  // update зберігає зміну однієї ролі (модель або режим сесії).
  async function update(role: string, patch: Partial<RoleSetting>) {
    if (!roles) return
    setSaving(role)
    setError(null)
    try {
      const saved = await api.saveSettings(roles.map((r) => (r.role === role ? { ...r, ...patch } : r)))
      setRoles(saved.roles)
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
        <div className="card-title"><Cpu size={18} /><h3>Моделі агентів</h3></div>
        <p className="muted" style={{ marginTop: 0 }}>
          З якою моделлю Claude стартує агент кожної ролі. ★ — рекомендовано для цієї ролі; наведи на модель, щоб
          побачити, кому вона підходить. Ціни — за цінами API за 1M токенів (вхід / вихід); з підпискою гроші не
          списуються, але дорожча модель швидше витрачає ліміт.
        </p>
        {roles === null && !error ? (
          <div className="skeleton" style={{ height: 240 }} />
        ) : (
          <div className="settings-list" style={{ marginBottom: 0 }}>
            {(roles ?? []).map((r) => {
              const current = MODELS[r.model]
              return (
                <div key={r.role} className="settings-row">
                  <RoleIcon role={r.role} />
                  <div className="settings-text">
                    <div className="settings-name">
                      {ROLE[r.role]?.label ?? r.role}
                      {saving === r.role && <RefreshCw size={14} className="spin muted" />}
                      {saved === r.role && <span className="badge tone-green"><Check size={12} /> збережено</span>}
                    </div>
                    {current && <div className="muted settings-hint">{current.label} · {current.price} — {current.hint}</div>}
                    {current?.warning && (
                      <div className="settings-warn"><TriangleAlert size={14} /> {current.warning}</div>
                    )}
                  </div>
                  <select className="input model-select" value={r.model} disabled={saving !== null}
                    aria-label={`Модель: ${ROLE[r.role]?.label ?? r.role}`} title={current?.hint}
                    onChange={(e) => update(r.role, { model: e.target.value })}>
                    {models.map((id) => {
                      const m = MODELS[id]
                      const best = m?.best.includes(r.role)
                      return (
                        <option key={id} value={id} title={m ? `${m.price} — ${m.hint}` : undefined}>
                          {m?.label ?? id}{best ? ' ★' : ''}{m ? ` · ${m.price}` : ''}
                        </option>
                      )
                    })}
                  </select>
                </div>
              )
            })}
          </div>
        )}
      </div>

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
                    onClick={() => !r.resume_session && update(r.role, { resume_session: true })} disabled={saving !== null}>
                    <RotateCcw size={14} /> Продовжує сесію
                  </button>
                  <button role="radio" aria-checked={!r.resume_session} className={`chip ${!r.resume_session ? 'active' : ''}`}
                    onClick={() => r.resume_session && update(r.role, { resume_session: false })} disabled={saving !== null}>
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
