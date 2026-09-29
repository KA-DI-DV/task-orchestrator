const rtf = new Intl.RelativeTimeFormat('uk', { numeric: 'auto' })

export function timeAgo(iso: string): string {
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000
  const abs = Math.abs(seconds)
  if (abs < 45) return 'щойно'
  if (abs < 3600) return rtf.format(Math.round(seconds / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(seconds / 3600), 'hour')
  return rtf.format(Math.round(seconds / 86400), 'day')
}

export function dateTime(iso: string): string {
  return new Date(iso).toLocaleString('uk', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function duration(ms: number): string {
  const total = Math.round(ms / 1000)
  if (total < 60) return `${total} с`
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  return h > 0 ? `${h} год ${m} хв` : `${m} хв ${total % 60} с`
}

export const money = (usd: number) => `$${usd.toFixed(2)}`

export const shortSha = (sha: string) => sha.slice(0, 8)

export function plural(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return one
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few
  return many
}

// Підказка до сум: Claude Code рахує вартість токенів за цінами API,
// навіть коли працює через підписку — тоді реально нічого не списується.
export const COST_HINT =
  'Скільки коштувала б ця робота за цінами Anthropic API. З підпискою Pro/Max гроші не списуються — ' +
  'це лише міра того, скільки ліміту підписки витратили агенти.'
