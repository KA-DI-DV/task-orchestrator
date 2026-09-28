import type { Status, TaskLog } from '../api'
import { PIPELINE, STATUS, STEP_STATUS } from '../meta'

// На якому етапі задача зараз (або на якому впала).
function currentIndex(status: Status, logs: TaskLog[]): { index: number; failed: boolean } {
  if (status === 'FAILED') {
    // Останній етап агента в історії — там і сталася проблема.
    const last = [...logs].reverse().find((l) => STEP_STATUS[l.step_name])
    const failedAt = last ? STEP_STATUS[last.step_name] : 'CREATED'
    return { index: PIPELINE.indexOf(failedAt), failed: true }
  }
  return { index: PIPELINE.indexOf(status), failed: false }
}

// Великий степер на сторінці задачі.
export function PipelineSteps({ status, logs }: { status: Status; logs: TaskLog[] }) {
  const { index, failed } = currentIndex(status, logs)
  return (
    <div className="card stepper">
      {PIPELINE.map((s, i) => {
        const m = STATUS[s]
        const Icon = m.icon
        const done = i < index || (s === 'COMPLETED' && status === 'COMPLETED')
        const now = i === index && !done
        const cls = done ? 'done' : now ? (failed ? 'fail' : 'now') : ''
        return (
          <div key={s} className={`step ${cls} tone-${m.tone}`}>
            <div className="step-dot"><Icon size={18} /></div>
            <div className="step-label">{m.label}</div>
          </div>
        )
      })}
    </div>
  )
}

// Маленький індикатор у списку задач: 4 риски (розробка, рев'ю, тест, готово).
// Для FAILED усі риски червоні: де саме впала, видно на сторінці задачі.
export function PipelineDots({ status }: { status: Status }) {
  const steps = PIPELINE.slice(1)
  const index = steps.indexOf(status)
  const tone = STATUS[status].tone
  return (
    <div className={`dots tone-${tone}`} aria-label={STATUS[status].label}>
      {steps.map((s, i) => {
        let cls = ''
        if (status === 'COMPLETED' || i < index) cls = 'done'
        else if (i === index) cls = 'now'
        else if (status === 'FAILED') cls = 'fail'
        return <i key={s} className={cls} />
      })}
    </div>
  )
}
