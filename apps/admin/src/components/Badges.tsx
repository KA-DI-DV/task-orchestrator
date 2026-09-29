import type { Status } from '../api'
import { isFinal, isWaiting } from '../api'
import { CirclePause } from 'lucide-react'
import { ROLE, STATUS, VERDICT } from '../meta'

// stopped — задача зупинилась посеред етапу: показуємо паузу замість «пульсу».
// Задача, що чекає на людину (план на апруві), теж без «пульсу» — агенти не працюють.
export function StatusBadge({ status, large, stopped }: { status: Status; large?: boolean; stopped?: boolean }) {
  const m = STATUS[status]
  const Icon = m.icon
  const size = large ? 16 : 14
  return (
    <span className={`badge tone-${stopped ? 'neutral' : m.tone} ${large ? 'badge-lg' : ''}`}>
      {stopped ? <CirclePause size={size} /> : isFinal(status) || isWaiting(status) ? <Icon size={size} /> : <span className="pulse" />}
      {stopped ? `Зупинено · ${m.label}` : m.label}
    </span>
  )
}

export function VerdictBadge({ verdict }: { verdict: string }) {
  const m = VERDICT[verdict]
  if (!m) return null
  const Icon = m.icon
  return (
    <span className={`badge tone-${m.tone}`}>
      <Icon size={14} /> {m.label}
    </span>
  )
}

export function RoleIcon({ role }: { role: string }) {
  const m = ROLE[role] ?? ROLE.orchestrator
  const Icon = m.icon
  return (
    <span className={`role-icon tone-${m.tone}`}>
      <Icon size={17} />
    </span>
  )
}
