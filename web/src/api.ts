import type {
  AppSettings,
  ArticlePreview,
  Candidate,
  Entry,
  RawItem,
  RunOutcome,
  RunRow,
  Source,
  SourceDefaults,
  SourceView,
  Stats,
  Suggestion,
  Target,
} from './types'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    ...init,
  })
  const text = await res.text()
  let data: unknown
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    const msg =
      data && typeof data === 'object' && 'error' in data
        ? String((data as { error: unknown }).error)
        : `请求失败（HTTP ${res.status}）`
    throw new Error(msg)
  }
  return data as T
}

export const api = {
  me: () => req<{ authenticated: boolean; auth_required: boolean }>('/api/me'),
  login: (password: string) =>
    req<{ ok: boolean }>('/api/login', { method: 'POST', body: JSON.stringify({ password }) }),
  logout: () => req<{ ok: boolean }>('/api/logout', { method: 'POST' }),

  sources: () => req<SourceView[]>('/api/sources'),
  source: (id: number) => req<SourceView>(`/api/sources/${id}`),
  createSource: (s: Source) =>
    req<SourceView>('/api/sources', { method: 'POST', body: JSON.stringify(s) }),
  updateSource: (s: Source) =>
    req<SourceView>(`/api/sources/${s.id}`, { method: 'PUT', body: JSON.stringify(s) }),
  deleteSource: (id: number) => req<{ ok: boolean }>(`/api/sources/${id}`, { method: 'DELETE' }),
  runSource: (id: number) =>
    req<RunOutcome>(`/api/sources/${id}/run`, { method: 'POST', body: '{}' }),
  tryDraft: (s: Source) =>
    req<{ items: RawItem[] | null; found: number; warnings: string[] | null; error?: string }>(
      '/api/try',
      {
        method: 'POST',
        body: JSON.stringify(s),
      },
    ),

  entries: (id: number, limit = 50, offset = 0) =>
    req<{ items: Entry[] | null; total: number }>(
      `/api/sources/${id}/entries?limit=${limit}&offset=${offset}`,
    ),
  deleteEntry: (id: number) => req<{ ok: boolean }>(`/api/entries/${id}`, { method: 'DELETE' }),
  runs: (sourceId = 0, limit = 50) =>
    req<RunRow[]>(`/api/runs?source_id=${sourceId}&limit=${limit}`),
  stats: () => req<Stats>('/api/stats'),
  transforms: () => req<{ ops: string[] }>('/api/transforms'),
  flushNotify: () => req<{ ok: boolean }>('/api/notify/flush', { method: 'POST', body: '{}' }),
  opmlUrl: '/api/export/opml',

  settings: () => req<AppSettings>('/api/settings'),
  saveSettings: (defaults: SourceDefaults) =>
    req<{ defaults: SourceDefaults }>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ defaults }),
    }),

  probeFetch: (body: {
    url: string
    engine: string
    kind?: string
    api?: {
      url: string
      method?: string
      headers?: Record<string, string>
      body?: string
      in_browser?: boolean
      wait_ms?: number
    }
    headers?: Record<string, string>
    cookies?: Record<string, string>
    proxy?: string
    wait_ms?: number
    wait_selector?: string
    timeout_sec?: number
    browser_ws?: string
    charset?: string
  }) =>
    req<{
      sid: string
      url: string
      title: string
      bytes: number
      kind: string
      warnings: string[] | null
    }>('/api/probe/fetch', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  probeArticle: (sid: string, article: Source['article']) =>
    req<ArticlePreview>('/api/probe/article', {
      method: 'POST',
      body: JSON.stringify({ sid, article }),
    }),
  probeExtract: (sid: string, extract: Source['extract'], rules: Source['rules']) =>
    req<{ items: RawItem[] | null; matched: number; warnings: string[] | null }>(
      '/api/probe/extract',
      {
        method: 'POST',
        body: JSON.stringify({ sid, extract, rules }),
      },
    ),
  probeSuggest: (sid: string, xpath: string, item = '') =>
    req<Suggestion>('/api/probe/suggest', {
      method: 'POST',
      body: JSON.stringify({ sid, xpath, item }),
    }),
  probeCount: (sid: string, selector: string) =>
    req<{ count: number; samples: string[] | null }>('/api/probe/count', {
      method: 'POST',
      body: JSON.stringify({ sid, selector }),
    }),

  targets: () => req<Target[]>('/api/targets'),
  createTarget: (t: { name: string; url: string }) =>
    req<Target>('/api/targets', { method: 'POST', body: JSON.stringify({ ...t, enabled: true }) }),
  updateTarget: (t: { id: number; name: string; url?: string; enabled: boolean }) =>
    req<{ ok: boolean }>(`/api/targets/${t.id}`, { method: 'PUT', body: JSON.stringify(t) }),
  deleteTarget: (id: number) => req<{ ok: boolean }>(`/api/targets/${id}`, { method: 'DELETE' }),
  testTargets: (body: { url?: string; ids?: number[] }) =>
    req<{ ok: boolean; sent: number }>('/api/targets/test', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  schemas: () => req<{ schemas: string[] }>('/api/schemas'),
}

export type { Candidate }
