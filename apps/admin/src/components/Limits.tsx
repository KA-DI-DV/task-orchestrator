import { useState } from 'react'
import { Gauge, RefreshCw, TriangleAlert } from 'lucide-react'
import { api, type ClaudeLimits, type LimitWindow } from '../api'
import { usePolling } from '../hooks'
import { dateTime, timeAgo } from '../format'

// Назви вікон лімітів підписки. Невідоме вікно показуємо під його ключем.
const WINDOW_LABEL: Record<string, string> = {
  five_hour: '5 годин',
  seven_day: 'Тиждень',
  seven_day_opus: 'Тиждень · Opus',
  seven_day_sonnet: 'Тиждень · Sonnet',
}
const WINDOW_ORDER = ['five_hour', 'seven_day', 'seven_day_opus', 'seven_day_sonnet']

// Блок у бічній панелі: скільки ліміту підписки Claude використано і коли він скинеться.
// Дані приходять з кожного запуску агента; «Оновити» робить окремий короткий запуск (haiku).
export function LimitsCard() {
  const limits = usePolling('limits', api.limits, 60_000)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function refresh() {
    setRefreshing(true)
    setError(null)
    try {
      await api.refreshLimits()
      await limits.refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setRefreshing(false)
    }
  }

  const data = limits.data
  const info = data?.info
  const windows = Object.entries(info?.unifiedWindows ?? {})
    .sort(([a], [b]) => order(a) - order(b))

  return (
    <div className="limits">
      <div className="limits-head">
        <span><Gauge size={15} /> Ліміти Claude</span>
        <button className="btn btn-ghost btn-icon btn-xs" onClick={refresh} disabled={refreshing}
          title="Оновити: короткий запуск claude (haiku) — сам теж трохи витрачає ліміт" aria-label="Оновити ліміти">
          <RefreshCw size={14} className={refreshing ? 'spin' : undefined} />
        </button>
      </div>

      {info && info.status !== 'allowed' && <LimitWarning info={info} />}

      {windows.length === 0 ? (
        <div className="limits-empty">
          {limits.data === null && !limits.error ? 'Завантажую…' : 'Дані з’являться після першого запуску агента або після «Оновити».'}
        </div>
      ) : (
        windows.map(([key, w]) => <WindowRow key={key} name={WINDOW_LABEL[key] ?? key} window={w} />)
      )}

      {(error || limits.error) && <div className="limits-error">{error ?? limits.error}</div>}
      {data && <div className="limits-foot">оновлено {timeAgo(data.observed_at)}</div>}
    </div>
  )
}

function WindowRow({ name, window: w }: { name: string; window: LimitWindow }) {
  const resetsAt = new Date(w.resetsAt * 1000)
  // Вікно вже скинулося після останнього виміру — старий відсоток нічого не каже.
  const expired = resetsAt.getTime() < Date.now()
  const pct = Math.round(w.utilization * 100)
  const tone = pct >= 90 ? 'red' : pct >= 70 ? 'amber' : 'green'

  return (
    <div className="limit-row">
      <div className="limit-top">
        <span>{name}</span>
        <b>{expired ? '—' : `${pct}%`}</b>
      </div>
      <div className={`limit-bar tone-${expired ? 'neutral' : tone}`}>
        <i style={{ width: `${expired ? 0 : Math.min(pct, 100)}%` }} />
      </div>
      <div className="limit-reset" title={resetsAt.toLocaleString('uk')}>
        {expired ? 'вже скинувся — натисни «Оновити»' : `скинеться ${timeAgo(resetsAt.toISOString())} · ${dateTime(resetsAt.toISOString())}`}
      </div>
    </div>
  )
}

function LimitWarning({ info }: { info: ClaudeLimits['info'] }) {
  const until = info.resetsAt ? dateTime(new Date(info.resetsAt * 1000).toISOString()) : null
  return (
    <div className="limits-warn">
      <TriangleAlert size={14} />
      <span>
        {info.status === 'rejected'
          ? `Ліміт вичерпано${until ? ` до ${until}` : ''} — агенти не зможуть працювати.`
          : 'Ліміт майже вичерпано.'}
      </span>
    </div>
  )
}

function order(key: string) {
  const i = WINDOW_ORDER.indexOf(key)
  return i < 0 ? WINDOW_ORDER.length : i
}
