export type Kind = 'html' | 'page' | 'json' | 'rss'
export type Engine = 'http' | 'browser'

export interface RequestSpec {
  headers: Record<string, string>
  cookies: Record<string, string>
  proxy: string
  timeout_sec: number
  charset: string
  browser_ws: string
  wait_ms: number
  wait_selector: string
  api?: APISpec
}

export interface APISpec {
  url: string
  method: string
  headers: Record<string, string>
  body: string
  in_browser: boolean
  wait_ms: number
}

export interface FieldSpec {
  sel: string
  attr: string
  absolute: boolean
  fallback: string[]
  optional: boolean
  template: string
  transforms: string[]
}

export interface ExtractSpec {
  item: string
  limit: number
  fields: Record<string, FieldSpec>
  scope: string
  exclude: string[]
  normalize: string[]
  ignore_regex: string[]
}

export interface Rule {
  id: string
  target: string[]
  op: string
  pattern: string
  replace: string
  flags: string
  value: string
  layouts: string[]
  tz: string
  n: number
}

export interface FilterSpec {
  include: string[]
  exclude: string[]
  field: string
  regex: boolean
}

export interface ArticleSpec {
  enabled: boolean
  content: string
  title: string
  date: string
  readability: boolean
  max_chars: number
}

export interface NotifyPolicy {
  triggers: string[]
  timing: string
  target_ids: number[]
  throttle_sec: number
  detail: string
  quiet_hours: string
  digest_at: string
  silent_after: number
}

export interface Policy {
  interval_sec: number
  cron: string
  max_items: number
  retention_days: number
  min_changed_lines: number
  update_mode: string
  notify: NotifyPolicy
}

export interface Source {
  id: number
  slug: string
  name: string
  kind: Kind
  url: string
  engine: Engine
  enabled: boolean
  request: RequestSpec
  extract: ExtractSpec
  rules: Rule[]
  filter: FilterSpec
  article: ArticleSpec
  policy: Policy
  created_at: number
  updated_at: number
  last_run_at: number
  next_run_at: number
  consecutive_failures: number
}

export interface SourceView extends Source {
  entry_count: number
  feed_rss: string
  feed_atom: string
  feed_json: string
}

export interface Entry {
  id: number
  source_id: number
  entry_key: string
  guid: string
  title: string
  link: string
  summary: string
  content_html: string
  content_text: string
  category: string
  published_at: number
  first_seen_at: number
  updated_at: number
  content_hash: string
  notified_at: number
}

export interface RunRow {
  id: number
  source_id: number
  started_at: number
  finished_at: number
  status: string
  trigger: string
  items_found: number
  items_new: number
  items_updated: number
  duration_ms: number
  error: string
  warnings: string[] | null
}

export interface Target {
  id: number
  name: string
  url?: string
  masked?: string
  kind: string
  enabled: boolean
  created_at: number
}

export interface RawItem {
  title: string
  link: string
  date: string
  summary: string
  category: string
  content: string
  guid: string
}

export interface Candidate {
  kind: string
  label: string
  xpath: string
  css: string
  count: number
}

export interface FieldGuess {
  name: string
  sel: string
  attr: string
  text: string
}

export interface Suggestion {
  candidates: Candidate[]
  fields: FieldGuess[]
  tag: string
  text: string
}

export interface RunOutcome {
  run: RunRow
  items: Entry[] | null
  updated: number
  warnings: string[] | null
  error?: string
}

export interface Stats {
  sources: number
  enabled: number
  entries: number
  new_today: number
  errors_24h: number
  empty_runs_24h: number
  last_run_at: number
  next_run_at: number
  targets: number
}

export interface LiveEvent {
  type: string
  at: number
  source_id?: number
  source_name?: string
  slug?: string
  run_id?: number
  status?: string
  found?: number
  new?: number
  updated?: number
  duration_ms?: number
  error?: string
  message?: string
}

export function blankSource(): Source {
  return {
    id: 0,
    slug: '',
    name: '',
    kind: 'html',
    url: '',
    engine: 'http',
    enabled: true,
    request: {
      headers: {},
      cookies: {},
      proxy: '',
      timeout_sec: 20,
      charset: '',
      browser_ws: '',
      wait_ms: 0,
      wait_selector: '',
    },
    extract: {
      item: '',
      limit: 50,
      fields: {},
      scope: '',
      exclude: [],
      normalize: [],
      ignore_regex: [],
    },
    rules: [],
    filter: { include: [], exclude: [], field: 'title', regex: false },
    article: {
      enabled: false,
      content: '',
      title: '',
      date: '',
      readability: true,
      max_chars: 4000,
    },
    policy: {
      interval_sec: 3600,
      cron: '',
      max_items: 50,
      retention_days: 0,
      min_changed_lines: 0,
      update_mode: 'update',
      notify: {
        triggers: ['new_entry', 'failure', 'recovery', 'silent_empty'],
        timing: 'immediate',
        target_ids: [],
        throttle_sec: 60,
        detail: 'summary',
        quiet_hours: '',
        digest_at: '09:00',
        silent_after: 3,
      },
    },
    created_at: 0,
    updated_at: 0,
    last_run_at: 0,
    next_run_at: 0,
    consecutive_failures: 0,
  }
}
