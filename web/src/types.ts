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

export interface SourceDefaults {
  interval_sec: number
  max_items: number
  retention_days: number
  update_mode: string
  article_enabled: boolean
}

// 间隔在界面上按分钟填写：本应用不需要秒级精度，后端仍以秒存储。
// 默认 3600 秒 = 60 分钟，不用再口算 4 小时是多少秒。
export const DEFAULT_INTERVAL_SEC = 3600
export const MIN_INTERVAL_MIN = 1

/** 把存储的秒换算成界面上的整分钟（老配置可能不是 60 的整数倍）。 */
export function intervalMinutes(sec: number): number {
  if (!Number.isFinite(sec) || sec <= 0) return DEFAULT_INTERVAL_SEC / 60
  return Math.max(MIN_INTERVAL_MIN, Math.round(sec / 60))
}

/** 把界面上的分钟换算成存储的秒，非法输入退回默认间隔。 */
export function intervalSeconds(min: number): number {
  if (!Number.isFinite(min) || min <= 0) return DEFAULT_INTERVAL_SEC
  return Math.max(MIN_INTERVAL_MIN, Math.round(min)) * 60
}

export interface SystemInfo {
  version: string
  base_url: string
  timezone: string
  workers: number
  data_dir: string
  auth_required: boolean
  feed_token_required: boolean
  allow_private: boolean
  browser_ws: string
  chrome_path: string
}

export interface AppSettings {
  defaults: SourceDefaults
  system: SystemInfo
}

export interface ArticlePreview {
  title: string
  date: string
  content_html: string
  content_text: string
  method: 'selector' | 'readability' | ''
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
      interval_sec: DEFAULT_INTERVAL_SEC,
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

// 只覆盖「新建源默认值」里暴露的五项，其余保持 blankSource。
export function applySourceDefaults(src: Source, defaults?: SourceDefaults): Source {
  if (!defaults) return src
  src.policy.interval_sec = defaults.interval_sec
  src.policy.max_items = defaults.max_items
  src.policy.retention_days = defaults.retention_days
  src.policy.update_mode = defaults.update_mode
  src.article.enabled = defaults.article_enabled
  return src
}
