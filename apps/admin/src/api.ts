// Типи відповідей REST API оркестратора (див. internal/storage/tasks.go)
// і функції для запитів.

export type Status =
  | 'CREATED' | 'IN_PLANNING' | 'PLAN_REVIEW' | 'IN_DEV' | 'IN_REVIEW' | 'IN_TEST' | 'COMPLETED' | 'FAILED'

export interface TaskRepo {
  repo_name: string
  worktree_path: string
  base_commit: string
  is_main: boolean
  commits: number
  head_commit: string
  diff_stat: string
}

export interface Task {
  id: string
  jira_url: string
  repo_name: string
  status: Status
  branch_name: string
  worktree_path: string
  review_attempts: number
  max_review_attempts: number
  last_feedback: string
  title: string
  description: string
  base_commit: string
  last_error: string
  test_report: string
  created_at: string
  updated_at: string
  completed_at: string | null
  total_cost_usd: number
  changed_repos: number
  agent_role: string // роль агента, чию сесію відновить «Продовжити» (порожньо — етап почнеться заново)
  agent_session: string
  running: boolean // задачу зараз виконують агенти
  plan: string // план Архітектора (markdown), порожній у старих задачах
  plan_revision: number // версія плану: 1, 2, ...
  plan_feedback: string // правки, які Архітектор зараз враховує
  plan_approved_at: string | null
  repos?: TaskRepo[]
}

export interface TaskLog {
  id: number
  task_id: string
  step_name: string
  agent_role: string
  iteration: number
  verdict: string
  prompt: string
  summary: string // повна фінальна відповідь агента
  brief: string // підсумок кроку простою мовою (порожній у старих записах)
  feedback: string
  cost_usd: number
  num_turns: number
  duration_ms: number
  output_log: string
  created_at: string
  session_mode: '' | 'new' | 'resume' | 'continue' // як агент працював із сесією ('' — старі записи)
}

export interface RoleSetting {
  role: string
  resume_session: boolean // коли задача повертається до ролі — продовжувати її сесію
}

export interface Info {
  main_repo: string
  repos: string[]
  exclude_repos: string[]
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  const body = await res.json().catch(() => null)
  if (!res.ok) {
    throw new Error(body?.error ?? `Сервер відповів ${res.status}`)
  }
  return body as T
}

export const api = {
  tasks: () => request<Task[] | null>('/api/tasks').then((t) => t ?? []),
  task: (id: string) => request<Task>(`/api/tasks/${id}`),
  logs: (id: string) => request<TaskLog[] | null>(`/api/tasks/${id}/logs`).then((l) => l ?? []),
  activity: (id: string) =>
    request<{ lines: string[] | null }>(`/api/tasks/${id}/activity`).then((a) => a.lines ?? []),
  info: () => request<Info>('/api/info'),
  settings: () => request<{ roles: RoleSetting[] }>('/api/settings').then((s) => s.roles),
  saveSettings: (roles: RoleSetting[]) =>
    request<{ roles: RoleSetting[] }>('/api/settings', { method: 'PUT', body: JSON.stringify({ roles }) })
      .then((s) => s.roles),
  create: (jiraUrl: string, repo: string) =>
    request<Task>('/api/tasks', {
      method: 'POST',
      body: JSON.stringify({ jira_url: jiraUrl, repo }),
    }),
  resume: (id: string) => request<Task>(`/api/tasks/${id}/resume`, { method: 'POST' }),
  approvePlan: (id: string) => request<Task>(`/api/tasks/${id}/plan/approve`, { method: 'POST' }),
  revisePlan: (id: string, comment: string) =>
    request<Task>(`/api/tasks/${id}/plan/revise`, { method: 'POST', body: JSON.stringify({ comment }) }),
  stop: (id: string) => request<unknown>(`/api/tasks/${id}/stop`, { method: 'POST' }),
  remove: (id: string) => request<null>(`/api/tasks/${id}`, { method: 'DELETE' }),
}

export const isFinal = (s: Status) => s === 'COMPLETED' || s === 'FAILED'

// Зупинена: етап не завершено, але агенти її зараз не виконують
// (зупинили з адмін-панелі або перезапускали контейнер).
export const isStopped = (t: Pick<Task, 'status' | 'running'>) =>
  !isFinal(t.status) && !isWaiting(t.status) && !t.running

// Чекає на людину: план готовий, треба схвалити його або надіслати правки.
export const isWaiting = (s: Status) => s === 'PLAN_REVIEW'
