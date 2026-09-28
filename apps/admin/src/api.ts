// Типи відповідей REST API оркестратора (див. internal/storage/tasks.go)
// і функції для запитів.

export type Status = 'CREATED' | 'IN_DEV' | 'IN_REVIEW' | 'IN_TEST' | 'COMPLETED' | 'FAILED'

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
  summary: string
  feedback: string
  cost_usd: number
  num_turns: number
  duration_ms: number
  output_log: string
  created_at: string
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
  create: (jiraUrl: string, repo: string) =>
    request<Task>('/api/tasks', {
      method: 'POST',
      body: JSON.stringify({ jira_url: jiraUrl, repo }),
    }),
  resume: (id: string) => request<Task>(`/api/tasks/${id}/resume`, { method: 'POST' }),
}

export const isFinal = (s: Status) => s === 'COMPLETED' || s === 'FAILED'
