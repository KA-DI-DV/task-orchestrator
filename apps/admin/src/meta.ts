// Людські назви, іконки і кольори для статусів, ролей і вердиктів — в одному місці,
// щоб усі сторінки виглядали однаково.
import {
  CircleCheck, CircleX, CodeXml, Cog, Eye, FlaskConical, Sparkles, CirclePause, type LucideIcon,
} from 'lucide-react'
import type { Status } from './api'

export type Tone = 'neutral' | 'blue' | 'amber' | 'plum' | 'green' | 'red' | 'accent'

interface Meta {
  label: string
  tone: Tone
  icon: LucideIcon
}

export const STATUS: Record<Status, Meta> = {
  CREATED: { label: 'Створено', tone: 'neutral', icon: Sparkles },
  IN_DEV: { label: 'Розробка', tone: 'blue', icon: CodeXml },
  IN_REVIEW: { label: "Рев'ю", tone: 'amber', icon: Eye },
  IN_TEST: { label: 'Тестування', tone: 'plum', icon: FlaskConical },
  COMPLETED: { label: 'Готово', tone: 'green', icon: CircleCheck },
  FAILED: { label: 'Впала', tone: 'red', icon: CircleX },
}

export const ROLE: Record<string, Meta> = {
  developer: { label: 'Розробник', tone: 'blue', icon: CodeXml },
  reviewer: { label: "Рев'юер", tone: 'amber', icon: Eye },
  tester: { label: 'QA', tone: 'plum', icon: FlaskConical },
  orchestrator: { label: 'Оркестратор', tone: 'neutral', icon: Cog },
}

export const VERDICT: Record<string, Meta> = {
  DONE: { label: 'Код готовий', tone: 'blue', icon: CircleCheck },
  APPROVE: { label: 'Схвалено', tone: 'green', icon: CircleCheck },
  CHANGES_REQUESTED: { label: 'Є зауваження', tone: 'amber', icon: Eye },
  PASSED: { label: 'Тести пройдено', tone: 'green', icon: CircleCheck },
  FAILED: { label: 'Тести не пройдено', tone: 'red', icon: CircleX },
  ERROR: { label: 'Помилка', tone: 'red', icon: CircleX },
  INTERRUPTED: { label: 'Перервано', tone: 'neutral', icon: CirclePause },
}

// Кроки конвеєра в порядку виконання (для степера).
export const PIPELINE: Status[] = ['CREATED', 'IN_DEV', 'IN_REVIEW', 'IN_TEST', 'COMPLETED']

// Який статус відповідає етапу з історії — щоб показати, де саме задача впала.
export const STEP_STATUS: Record<string, Status> = {
  DEVELOPMENT: 'IN_DEV',
  REVIEW: 'IN_REVIEW',
  TESTING: 'IN_TEST',
}
