import type { Status } from '../api'
import { isFinal } from '../api'
import { ROLE, STATUS, VERDICT } from '../meta'

export function StatusBadge({ status, large }: { status: Status; large?: boolean }) {
  const m = STATUS[status]
  const Icon = m.icon
  return (
    <span className={`badge tone-${m.tone} ${large ? 'badge-lg' : ''}`}>
      {isFinal(status) ? <Icon size={large ? 16 : 14} /> : <span className="pulse" />}
      {m.label}
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
