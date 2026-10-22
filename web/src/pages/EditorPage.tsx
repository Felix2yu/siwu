import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import {
  ArrowLeft,
  Crosshair,
  Loader2,
  Locate,
  Maximize2,
  Minimize2,
  Play,
  RefreshCw,
  Save,
  Trash2,
} from 'lucide-react'
import { api } from '../api'
import { PageHead } from '../App'
import {
  Badge,
  Button,
  Checkbox,
  Field,
  Input,
  Select,
  Switch,
  TextArea,
  cn,
} from '../components/ui'
import type { ArticlePreview, Candidate, FieldSpec, RawItem, Source, Suggestion } from '../types'
import { applySourceDefaults, blankSource } from '../types'
import type { SourceView } from '../types'

const FIELD_NAMES = ['title', 'link', 'date', 'summary', 'category', 'content'] as const
// 兜底列表：服务端 /api/transforms 会返回权威全集
const OPS = [
  'regex_replace',
  'regex_extract',
  'trim',
  'collapse_ws',
  'lower',
  'upper',
  'strip_prefix',
  'strip_suffix',
  'replace_literal',
  'date_parse',
  'truncate',
  'remove_tags',
  'html_unescape',
]

const TRIGGERS = [
  { value: 'new_entry', label: '有新条目' },
  { value: 'content_changed', label: '内容被修改' },
  { value: 'failure', label: '运行失败' },
  { value: 'recovery', label: '故障恢复' },
  { value: 'silent_empty', label: '连续空跑' },
]

function linesToMap(raw: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of raw.split('\n')) {
    const i = line.indexOf(':')
    if (i <= 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (k) out[k] = v
  }
  return out
}

function mapToLines(map: Record<string, string> | undefined): string {
  if (!map) return ''
  return Object.entries(map)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n')
}

function emptyField(): FieldSpec {
  return {
    sel: '',
    attr: '',
    absolute: false,
    fallback: [],
    optional: false,
    template: '',
    transforms: [],
  }
}

function toSource(v: SourceView): Source {
  const { entry_count: _ec, feed_rss: _fr, feed_atom: _fa, feed_json: _fj, ...src } = v
  return src
}

export default function EditorPage() {
  const { id } = useParams()
  const editing = Boolean(id)
  const existing = useQuery({
    queryKey: ['source', Number(id)],
    queryFn: () => api.source(Number(id)),
    enabled: editing,
  })
  const appSettings = useQuery({
    queryKey: ['settings'],
    queryFn: api.settings,
    enabled: !editing,
  })

  if (editing && existing.isLoading) {
    return <div className="p-6 text-[12px] text-faint">加载中…</div>
  }
  if (editing && (existing.isError || !existing.data)) {
    return (
      <div className="p-6 text-[12px] text-danger">
        读取失败：{existing.error?.message ?? '源不存在'}
      </div>
    )
  }
  if (!editing && appSettings.isLoading) {
    return <div className="p-6 text-[12px] text-faint">加载中…</div>
  }

  // 用 key 强制重建表单状态，避免「数据到了再 setState」的 effect（会引发级联渲染）
  return (
    <EditorForm
      key={editing ? `source-${id}` : 'source-new'}
      initial={
        existing.data
          ? toSource(existing.data)
          : applySourceDefaults(blankSource(), appSettings.data?.defaults)
      }
      editing={editing}
    />
  )
}

function EditorForm({ initial, editing }: { initial: Source; editing: boolean }) {
  const { id } = useParams()
  const nav = useNavigate()
  const qc = useQueryClient()

  const [draft, setDraft] = useState<Source>(initial)
  const [headersText, setHeadersText] = useState(() => mapToLines(initial.request.headers))
  const [sid, setSid] = useState('')
  const [picked, setPicked] = useState('')
  // 预览形态：接口响应渲染成可点选的 JSON 树，HTML 页面才是框选
  const [previewKind, setPreviewKind] = useState<'html' | 'json'>('html')
  const [previewWarn, setPreviewWarn] = useState<string[]>([])
  const [previewMeta, setPreviewMeta] = useState<{ title: string; bytes: number } | null>(null)
  const [suggestion, setSuggestion] = useState<Suggestion | null>(null)
  const [probeItems, setProbeItems] = useState<RawItem[]>([])
  const [probeWarn, setProbeWarn] = useState<string[]>([])
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState('')
  const [pickTarget, setPickTarget] = useState<'item' | 'article'>('item')
  // 预览宽度：移动端 H5 按桌面宽度渲染会把列表挤出首屏，必须能切到手机宽度
  const [viewWidth, setViewWidth] = useState<'auto' | '390' | '768'>('auto')
  const [expand, setExpand] = useState(false)
  const [detail, setDetail] = useState<{ sid: string; url: string; title: string } | null>(null)
  const [articleLink, setArticleLink] = useState('')
  const [articlePicked, setArticlePicked] = useState('')
  const [articleView, setArticleView] = useState<ArticlePreview | null>(null)
  const [articleError, setArticleError] = useState('')
  const frameRef = useRef<HTMLIFrameElement>(null)

  const targets = useQuery({ queryKey: ['targets'], queryFn: api.targets })
  const transformOps = useQuery({ queryKey: ['transforms'], queryFn: api.transforms })
  const ops = transformOps.data?.ops?.length ? transformOps.data.ops : OPS

  const isJSON = draft.kind === 'json'
  const isHTML = draft.kind === 'html'
  const isPage = draft.kind === 'page'

  function patch(fn: (d: Source) => void) {
    setDraft((prev) => {
      const next = structuredClone(prev)
      fn(next)
      return next
    })
  }

  async function pickElement(xpath: string, isJSON = false) {
    if (pickTarget === 'article') {
      if (!detail) return
      try {
        setArticlePicked(xpath)
        const next = { ...draft.article, content: xpath }
        patch((d) => void (d.article.content = xpath))
        setMessage('已把选中元素写入正文选择器')
        await runArticlePreview(detail.sid, next)
      } catch (err) {
        setMessage(err instanceof Error ? err.message : '正文抽取失败')
      }
      return
    }
    if (!sid) return
    try {
      setBusy('suggest')
      setPicked(xpath)
      const sug = await api.probeSuggest(sid, xpath, draft.extract.item)
      setSuggestion(sug)
      setMessage(
        isJSON ? `已选中 ${xpath}，候选与字段推断见下方` : `已选中 <${sug.tag}>，命中候选见右侧`,
      )
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '解析选中元素失败')
    } finally {
      setBusy('')
    }
  }

  // 以「条目节点」为根重新推断字段，再抽取。框选后点候选、或改完条目选择器都能复用。
  async function inferAndExtract(itemSel: string) {
    let sug = suggestion
    if (sid && picked) {
      try {
        sug = await api.probeSuggest(sid, picked, itemSel)
        setSuggestion(sug)
      } catch {
        /* 推断失败不影响手动填写 */
      }
    }
    const fields = { ...draft.extract.fields }
    if (sug) {
      for (const g of sug.fields) {
        if (!FIELD_NAMES.includes(g.name as (typeof FIELD_NAMES)[number])) continue
        fields[g.name] = { ...(fields[g.name] ?? emptyField()), sel: g.sel, attr: g.attr }
      }
    }
    patch((d) => {
      d.extract.item = itemSel
      d.extract.fields = fields
    })
    await runProbeExtract({ ...draft.extract, item: itemSel, fields }, draft.rules)
  }

  function rememberArticleLink(items: RawItem[] | null | undefined) {
    const link = (items ?? []).find((it) => it.link)?.link
    if (!link) return
    let abs = link
    try {
      abs = new URL(link, draft.url || undefined).toString()
    } catch {
      /* 相对链接补全失败就原样保留 */
    }
    setArticleLink((prev) => prev || abs)
  }

  async function runArticlePreview(sessionSid = detail?.sid ?? '', spec = draft.article) {
    if (!sessionSid) {
      setMessage('先点「预览正文」抓取一条详情页')
      return
    }
    try {
      setBusy('article')
      setArticleError('')
      const res = await api.probeArticle(sessionSid, spec)
      setArticleView(res)
      const chars = res.content_text.length
      setMessage(
        res.method === 'readability'
          ? `readability 识别到正文，${chars} 字`
          : `正文选择器命中，${chars} 字`,
      )
    } catch (err) {
      const text = err instanceof Error ? err.message : '正文抽取失败'
      setArticleView(null)
      setArticleError(text)
      setMessage(text)
    } finally {
      setBusy('')
    }
  }

  async function openDetail() {
    const link = articleLink.trim()
    if (!link) {
      setMessage('先填一条详情页地址，或先抽取条目自动带入')
      return
    }
    try {
      setBusy('detail')
      setMessage('')
      setArticleError('')
      setArticleView(null)
      setArticlePicked('')
      const res = await api.probeFetch({
        url: link,
        engine: draft.engine,
        headers: linesToMap(headersText),
        cookies: draft.request.cookies,
        proxy: draft.request.proxy,
        wait_ms: draft.request.wait_ms,
        wait_selector: draft.request.wait_selector,
        timeout_sec: draft.request.timeout_sec,
        browser_ws: draft.request.browser_ws,
        charset: draft.request.charset,
      })
      setDetail({ sid: res.sid, url: res.url, title: res.title })
      setPickTarget('article')
      setMessage('详情页已载入：在右侧点选正文容器，或在下方点「重新识别正文」')
      await runArticlePreview(res.sid, draft.article)
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '详情页抓取失败')
    } finally {
      setBusy('')
    }
  }

  // 拾取器通过 postMessage 回传选中的元素；用 Effect Event 读最新 state 而不重订阅
  const onPicked = useEffectEvent((xpath: string, isJSON: boolean) => {
    void pickElement(xpath, isJSON)
  })

  useEffect(() => {
    function onMessage(e: MessageEvent) {
      const data = e.data as {
        source?: string
        type?: string
        xpath?: string
        json?: boolean
      } | null
      if (!data || data.source !== 'siwu-picker') return
      // 正文预览可能没经过列表预览，不能用 sid 作为拾取器的前置条件
      if (data.type === 'picked' && data.xpath) onPicked(data.xpath, !!data.json)
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
  }, [])

  async function fetchPreview() {
    if (!draft.url) {
      setMessage('先填写页面地址')
      return
    }
    try {
      setBusy('preview')
      setMessage('')
      const res = await api.probeFetch({
        url: draft.url,
        engine: draft.engine,
        kind: draft.kind,
        api: draft.request.api,
        headers: linesToMap(headersText),
        cookies: draft.request.cookies,
        proxy: draft.request.proxy,
        wait_ms: draft.request.wait_ms,
        wait_selector: draft.request.wait_selector,
        timeout_sec: draft.request.timeout_sec,
        browser_ws: draft.request.browser_ws,
        charset: draft.request.charset,
      })
      setSid(res.sid)
      setPreviewKind(res.kind === 'json' ? 'json' : 'html')
      setPreviewWarn(res.warnings ?? [])
      setPreviewMeta({ title: res.title, bytes: res.bytes })
      setSuggestion(null)
      setProbeItems([])
      setPickTarget('item')
      setDetail(null)
      setArticleView(null)
      setArticleError('')
      setMessage(
        res.kind === 'json'
          ? '接口响应已展开：点任意一行选中该路径（点 ▸ 展开层级）'
          : '预览已就绪：在右侧页面里点击要监控的条目',
      )
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '抓取失败')
    } finally {
      setBusy('')
    }
  }

  async function runProbeExtract(nextExtract = draft.extract, nextRules = draft.rules) {
    if (!sid) return
    try {
      setBusy('extract')
      const res = await api.probeExtract(sid, nextExtract, nextRules)
      const items = (res.items ?? []) as unknown as RawItem[]
      setProbeItems(items)
      rememberArticleLink(items)
      setProbeWarn(res.warnings ?? [])
      setMessage(`抽取命中 ${res.matched} 个节点，产出 ${res.items?.length ?? 0} 条`)
      // 只要命中了节点就滚过去：定位看的是 DOM，跟字段填没填全无关
      if (res.matched > 0) scrollToFirstMatch(nextExtract.item)
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '抽取失败')
    } finally {
      setBusy('')
    }
  }

  // 让预览 iframe 把第一条命中滚进视野：抽到不等于看得见
  function scrollToFirstMatch(sel = draft.extract.item) {
    if (!sel) return
    frameRef.current?.contentWindow?.postMessage(
      { source: 'siwu-host', type: 'scrollTo', sel },
      '*',
    )
  }

  function applyCandidate(c: Candidate) {
    void inferAndExtract(c.xpath)
  }

  function applyFieldGuesses() {
    void inferAndExtract(draft.extract.item)
  }

  const save = useMutation({
    mutationFn: () => {
      const payload = structuredClone(draft)
      payload.request.headers = linesToMap(headersText)
      return editing ? api.updateSource(payload) : api.createSource(payload)
    },
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['sources'] })
      setMessage(`已保存：${res.slug}`)
      if (!editing) nav(`/sources/${res.id}`)
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '保存失败'),
  })

  const tryRun = useMutation({
    mutationFn: () => {
      const payload = structuredClone(draft)
      payload.request.headers = linesToMap(headersText)
      return api.tryDraft(payload)
    },
    onSuccess: (res) => {
      const items = (res.items ?? []) as unknown as RawItem[]
      setProbeItems(items)
      rememberArticleLink(items)
      setProbeWarn(res.warnings ?? [])
      setMessage(res.error ? `试跑失败：${res.error}` : `试跑完成：${res.found} 条`)
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '试跑失败'),
  })

  const targetList = targets.data ?? []
  const rows = useMemo(() => (Array.isArray(probeItems) ? probeItems : []), [probeItems])
  // 接口响应也走 iframe：预览渲染成可点选的 JSON 树
  const frameSid =
    pickTarget === 'article' && detail
      ? detail.sid
      : isHTML || isPage || previewKind === 'json'
        ? sid
        : ''

  return (
    <div className="flex h-full flex-col">
      <PageHead
        title={editing ? `编辑 · ${draft.name || draft.slug}` : '新建监控源'}
        desc="填写地址 → 抓取预览 → 框选条目 → 核对抽取结果 → 保存"
        actions={
          <>
            <Button variant="ghost" onClick={() => nav('/')}>
              返回
            </Button>
            <Button
              variant="default"
              onClick={() => tryRun.mutate()}
              disabled={tryRun.isPending || !draft.url}
              title="用当前配置真抓一次，不写库"
            >
              {tryRun.isPending ? (
                <Loader2 size={13} className="animate-spin" />
              ) : (
                <Play size={13} />
              )}{' '}
              试跑
            </Button>
            <Button variant="primary" onClick={() => save.mutate()} disabled={save.isPending}>
              {save.isPending ? <Loader2 size={13} className="animate-spin" /> : <Save size={13} />}{' '}
              保存
            </Button>
          </>
        }
      />

      {message ? (
        <div className="border-b border-line-soft bg-panel/40 px-5 py-1.5 text-[12px] text-muted">
          {message}
        </div>
      ) : null}

      <div className="grid min-h-0 flex-1 grid-cols-[minmax(360px,420px)_1fr]">
        {/* 左：配置 */}
        <div className="min-h-0 overflow-y-auto border-r border-line-soft px-4 py-3">
          <section className="space-y-2">
            <Field label="名称">
              <Input
                value={draft.name}
                onChange={(e) => patch((d) => void (d.name = e.target.value))}
              />
            </Field>
            <Field label="页面地址" hint="列表型填列表页；接口型填触发风控的前置页面">
              <Input
                value={draft.url}
                placeholder="https://example.com/news"
                onChange={(e) => patch((d) => void (d.url = e.target.value))}
              />
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="形态">
                <Select
                  value={draft.kind}
                  onChange={(e) =>
                    patch((d) => {
                      d.kind = e.target.value as Source['kind']
                      // 切到接口型时先建好 api 骨架，否则下面的输入框改了也不生效
                      if (d.kind === 'json' && !d.request.api) {
                        d.request.api = {
                          url: '',
                          method: 'GET',
                          headers: {},
                          body: '',
                          in_browser: true,
                          wait_ms: 5000,
                        }
                      }
                    })
                  }
                >
                  <option value="html">列表（HTML）</option>
                  <option value="json">接口（JSON）</option>
                  <option value="page">整页 / 区域快照</option>
                  <option value="rss">已有 RSS</option>
                </Select>
              </Field>
              <Field label="抓取通道">
                <Select
                  value={draft.engine}
                  onChange={(e) =>
                    patch((d) => {
                      d.engine = e.target.value as Source['engine']
                    })
                  }
                >
                  <option value="http">HTTP（快，静态页够用）</option>
                  <option value="browser">浏览器（JS 渲染 / 风控）</option>
                </Select>
              </Field>
            </div>
            <div className="flex items-center gap-3 pt-1">
              <Checkbox
                label="启用定时"
                checked={draft.enabled}
                onChange={(e) => patch((d) => void (d.enabled = e.target.checked))}
              />
              <Field label="间隔（秒）" className="w-28">
                <Input
                  type="number"
                  value={draft.policy.interval_sec}
                  onChange={(e) =>
                    patch((d) => void (d.policy.interval_sec = Number(e.target.value)))
                  }
                />
              </Field>
              <Field label="输出条数" className="w-24">
                <Input
                  type="number"
                  value={draft.policy.max_items}
                  onChange={(e) => patch((d) => void (d.policy.max_items = Number(e.target.value)))}
                />
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Field label="cron 表达式" hint="如 0 9 * * 1-5，优先于间隔">
                <Input
                  value={draft.policy.cron}
                  placeholder="留空则用间隔"
                  className="font-mono text-[12px]"
                  onChange={(e) => patch((d) => void (d.policy.cron = e.target.value))}
                />
              </Field>
              <Field label="保留天数" hint="0 表示永久保留">
                <Input
                  type="number"
                  value={draft.policy.retention_days}
                  onChange={(e) =>
                    patch((d) => void (d.policy.retention_days = Number(e.target.value)))
                  }
                />
              </Field>
            </div>
          </section>

          <details className="mt-3 rounded border border-line-soft bg-panel/40 px-3 py-2">
            <summary className="cursor-pointer text-[12px] text-muted">
              抓取参数（请求头 / 等待 / 代理）
            </summary>
            <div className="mt-2 space-y-2">
              <Field label="请求头" hint="每行一条：Key: Value">
                <TextArea
                  rows={3}
                  value={headersText}
                  placeholder={'Referer: https://example.com\nAccept-Language: zh-CN'}
                  onChange={(e) => setHeadersText(e.target.value)}
                />
              </Field>
              <div className="grid grid-cols-2 gap-2">
                <Field label="渲染后等待（ms）" hint="浏览器通道有效；0 用默认 3000ms">
                  <Input
                    type="number"
                    value={draft.request.wait_ms}
                    onChange={(e) =>
                      patch((d) => void (d.request.wait_ms = Number(e.target.value)))
                    }
                  />
                </Field>
                <Field label="等待选择器">
                  <Input
                    value={draft.request.wait_selector}
                    placeholder="css:.list"
                    onChange={(e) => patch((d) => void (d.request.wait_selector = e.target.value))}
                  />
                </Field>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <Field label="代理" hint="留空走系统环境变量">
                  <Input
                    value={draft.request.proxy}
                    placeholder="http://127.0.0.1:7890"
                    className="font-mono text-[12px]"
                    onChange={(e) => patch((d) => void (d.request.proxy = e.target.value))}
                  />
                </Field>
                <Field label="超时（秒）">
                  <Input
                    type="number"
                    value={draft.request.timeout_sec}
                    onChange={(e) =>
                      patch((d) => void (d.request.timeout_sec = Number(e.target.value)))
                    }
                  />
                </Field>
              </div>
              <Field label="浏览器 CDP 地址" hint="留空则用本机 Chrome / 全局 SIWU_BROWSER_WS">
                <Input
                  value={draft.request.browser_ws}
                  placeholder="ws://browser:9222"
                  onChange={(e) => patch((d) => void (d.request.browser_ws = e.target.value))}
                />
              </Field>
            </div>
          </details>

          {isJSON ? (
            <section className="mt-3 space-y-2 rounded border border-line-soft bg-panel/40 px-3 py-2">
              <p className="text-[11px] uppercase tracking-wide text-muted">接口</p>
              <Field label="API 地址">
                <Input
                  value={draft.request.api?.url ?? ''}
                  onChange={(e) =>
                    patch((d) => {
                      d.request.api = {
                        ...(d.request.api ?? {
                          url: '',
                          method: 'GET',
                          headers: {},
                          body: '',
                          in_browser: true,
                          wait_ms: 5000,
                        }),
                        url: e.target.value,
                      }
                    })
                  }
                />
              </Field>
              <div className="grid grid-cols-2 gap-2">
                <Field label="方法">
                  <Select
                    value={draft.request.api?.method ?? 'GET'}
                    onChange={(e) =>
                      patch((d) => {
                        if (d.request.api) d.request.api.method = e.target.value
                      })
                    }
                  >
                    <option>GET</option>
                    <option>POST</option>
                  </Select>
                </Field>
                <Field label="风控等待（ms）">
                  <Input
                    type="number"
                    value={draft.request.api?.wait_ms ?? 5000}
                    onChange={(e) =>
                      patch((d) => {
                        if (d.request.api) d.request.api.wait_ms = Number(e.target.value)
                      })
                    }
                  />
                </Field>
              </div>
              <Checkbox
                label="在浏览器页面上下文里请求（穿透 WAF 签名）"
                checked={draft.request.api?.in_browser ?? false}
                onChange={(e) =>
                  patch((d) => {
                    if (d.request.api) d.request.api.in_browser = e.target.checked
                  })
                }
              />
              <Field label="接口请求头" hint="每行一条，如 Origin: https://site">
                <TextArea
                  rows={2}
                  value={mapToLines(draft.request.api?.headers)}
                  onChange={(e) =>
                    patch((d) => {
                      if (d.request.api) d.request.api.headers = linesToMap(e.target.value)
                    })
                  }
                />
              </Field>
              <Field label="item 路径（JSON）">
                <Input
                  value={draft.extract.item}
                  placeholder="$.data.dataList"
                  onChange={(e) => patch((d) => void (d.extract.item = e.target.value))}
                />
              </Field>
            </section>
          ) : null}

          {isPage ? (
            <section className="mt-3 space-y-2">
              <Field label="监控区域选择器" hint="留空则监控整页">
                <Input
                  value={draft.extract.scope}
                  placeholder="xpath://div[@id='main']"
                  onChange={(e) => patch((d) => void (d.extract.scope = e.target.value))}
                />
              </Field>
              <div className="grid grid-cols-2 gap-2">
                <Field label="变化行数阈值" hint="低于该值视为未变化，0=不启用">
                  <Input
                    type="number"
                    value={draft.policy.min_changed_lines}
                    onChange={(e) =>
                      patch((d) => void (d.policy.min_changed_lines = Number(e.target.value)))
                    }
                  />
                </Field>
                <Field label="排除区域" hint="每行一个选择器，如 css:.ad">
                  <TextArea
                    rows={2}
                    value={draft.extract.exclude.join('\n')}
                    placeholder="css:.sidebar-ad"
                    onChange={(e) =>
                      patch(
                        (d) =>
                          void (d.extract.exclude = e.target.value.split('\n').filter(Boolean)),
                      )
                    }
                  />
                </Field>
              </div>
              <Field label="忽略变化的正则" hint="每行一条，用于剔除访问量、时间戳等噪声">
                <TextArea
                  rows={2}
                  value={draft.extract.ignore_regex.join('\n')}
                  onChange={(e) =>
                    patch(
                      (d) =>
                        void (d.extract.ignore_regex = e.target.value.split('\n').filter(Boolean)),
                    )
                  }
                />
              </Field>
            </section>
          ) : null}

          {isHTML || draft.kind === 'rss' ? (
            <section className="mt-3 space-y-2">
              {isHTML ? (
                <>
                  <Field label="条目选择器" hint="框选后自动填入；也可手写 xpath: 或 css:">
                    <Input
                      value={draft.extract.item}
                      placeholder="xpath://div[@class='list']//li"
                      onChange={(e) => patch((d) => void (d.extract.item = e.target.value))}
                    />
                  </Field>
                  <Field label="排除条目" hint="命中这些选择器的条目会被丢弃，如 css:li.ad">
                    <TextArea
                      rows={2}
                      value={draft.extract.exclude.join('\n')}
                      onChange={(e) =>
                        patch(
                          (d) =>
                            void (d.extract.exclude = e.target.value.split('\n').filter(Boolean)),
                        )
                      }
                    />
                  </Field>
                </>
              ) : null}
              <Field label="判重时忽略的正则" hint="每行一条；命中的片段不参与内容哈希，如访问量">
                <TextArea
                  rows={2}
                  value={draft.extract.normalize.join('\n')}
                  onChange={(e) =>
                    patch(
                      (d) =>
                        void (d.extract.normalize = e.target.value.split('\n').filter(Boolean)),
                    )
                  }
                />
              </Field>
            </section>
          ) : null}

          {!isPage && draft.kind !== 'rss' ? (
            <section className="mt-3">
              <div className="mb-1 flex items-center justify-between">
                <p className="text-[11px] uppercase tracking-wide text-muted">字段</p>
                <Button size="sm" variant="ghost" onClick={() => void runProbeExtract()}>
                  重新抽取
                </Button>
              </div>
              <div className="space-y-1.5">
                {FIELD_NAMES.map((name) => {
                  const f = draft.extract.fields[name] ?? emptyField()
                  return (
                    <div key={name} className="grid grid-cols-[62px_1fr_82px] items-center gap-1.5">
                      <span className="font-mono text-[11px] text-muted">{name}</span>
                      <Input
                        value={isJSON ? f.template || f.sel : f.sel}
                        placeholder={isJSON ? '$.field 或模板' : 'css:a / .//a'}
                        className="font-mono text-[12px]"
                        onChange={(e) =>
                          patch((d) => {
                            const cur = d.extract.fields[name] ?? emptyField()
                            if (isJSON) {
                              if (e.target.value.includes('{')) cur.template = e.target.value
                              else {
                                cur.sel = e.target.value
                                cur.template = ''
                              }
                            } else {
                              cur.sel = e.target.value
                            }
                            d.extract.fields[name] = cur
                          })
                        }
                      />
                      {isJSON ? (
                        <span className="text-[11px] text-faint">路径</span>
                      ) : (
                        <Input
                          value={f.attr}
                          placeholder="href"
                          className="font-mono text-[12px]"
                          onChange={(e) =>
                            patch((d) => {
                              const cur = d.extract.fields[name] ?? emptyField()
                              cur.attr = e.target.value
                              d.extract.fields[name] = cur
                            })
                          }
                        />
                      )}
                    </div>
                  )
                })}
              </div>
            </section>
          ) : null}

          <section className="mt-3">
            <div className="mb-1 flex items-center justify-between">
              <p className="text-[11px] uppercase tracking-wide text-muted">清洗规则</p>
              <Button
                size="sm"
                variant="ghost"
                onClick={() =>
                  patch((d) => {
                    d.rules.push({
                      id: `r${d.rules.length + 1}`,
                      target: ['title'],
                      op: 'regex_replace',
                      pattern: '',
                      replace: '',
                      flags: 'i',
                      value: '',
                      layouts: [],
                      tz: 'Asia/Shanghai',
                      n: 0,
                    })
                  })
                }
              >
                + 规则
              </Button>
            </div>
            <div className="space-y-1.5">
              {draft.rules.length === 0 ? (
                <p className="text-[11px] text-faint">
                  例：去掉标题末尾的 NEW —— 正则 <code className="font-mono">\s*NEW\s*$</code>
                  ，替换留空。
                </p>
              ) : null}
              {draft.rules.map((r, idx) => (
                <div key={idx} className="rounded border border-line-soft bg-panel/40 p-1.5">
                  <div className="flex items-center gap-1.5">
                    <Input
                      value={r.id}
                      className="w-20 font-mono text-[11px]"
                      onChange={(e) => patch((d) => void (d.rules[idx]!.id = e.target.value))}
                    />
                    <Select
                      value={r.op}
                      className="w-36 text-[12px]"
                      onChange={(e) => patch((d) => void (d.rules[idx]!.op = e.target.value))}
                    >
                      {ops.map((op) => (
                        <option key={op} value={op}>
                          {op}
                        </option>
                      ))}
                    </Select>
                    <Input
                      value={r.target.join(',')}
                      placeholder="title"
                      className="w-24 font-mono text-[11px]"
                      onChange={(e) =>
                        patch(
                          (d) =>
                            void (d.rules[idx]!.target = e.target.value.split(',').filter(Boolean)),
                        )
                      }
                    />
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => patch((d) => void d.rules.splice(idx, 1))}
                    >
                      <Trash2 size={12} />
                    </Button>
                  </div>
                  {r.op === 'regex_replace' || r.op === 'regex_extract' ? (
                    <div className="mt-1 grid grid-cols-2 gap-1.5">
                      <Input
                        value={r.pattern}
                        placeholder="正则"
                        className="font-mono text-[11px]"
                        onChange={(e) =>
                          patch((d) => void (d.rules[idx]!.pattern = e.target.value))
                        }
                      />
                      <Input
                        value={r.replace}
                        placeholder="替换为（可空）"
                        className="font-mono text-[11px]"
                        onChange={(e) =>
                          patch((d) => void (d.rules[idx]!.replace = e.target.value))
                        }
                      />
                    </div>
                  ) : null}
                  {r.op === 'date_parse' ? (
                    <Input
                      value={r.layouts.join(',')}
                      placeholder="2006-01-02,2006/01/02"
                      className="mt-1 font-mono text-[11px]"
                      onChange={(e) =>
                        patch(
                          (d) =>
                            void (d.rules[idx]!.layouts = e.target.value
                              .split(',')
                              .filter(Boolean)),
                        )
                      }
                    />
                  ) : null}
                  {r.op === 'strip_prefix' ||
                  r.op === 'strip_suffix' ||
                  r.op === 'replace_literal' ? (
                    <Input
                      value={r.value}
                      placeholder="文本"
                      className="mt-1 font-mono text-[11px]"
                      onChange={(e) => patch((d) => void (d.rules[idx]!.value = e.target.value))}
                    />
                  ) : null}
                </div>
              ))}
            </div>
          </section>

          <section className="mt-3 space-y-2 rounded border border-line-soft bg-panel/40 px-3 py-2">
            <p className="text-[11px] uppercase tracking-wide text-muted">过滤与去重</p>
            <div className="grid grid-cols-2 gap-2">
              <Field label="去重策略" hint="内容变更时同步 / 只看新增 / 变更留版本">
                <Select
                  value={draft.policy.update_mode}
                  onChange={(e) => patch((d) => void (d.policy.update_mode = e.target.value))}
                >
                  <option value="update">内容变更时更新</option>
                  <option value="ignore">只看新增</option>
                  <option value="archive">变更留版本</option>
                </Select>
              </Field>
              <Field label="匹配字段">
                <Select
                  value={draft.filter.field}
                  onChange={(e) => patch((d) => void (d.filter.field = e.target.value))}
                >
                  <option value="title">标题</option>
                  <option value="summary">摘要</option>
                  <option value="link">链接</option>
                  <option value="category">分类</option>
                </Select>
              </Field>
            </div>
            <Field label="必须包含" hint="每行一条；留空表示不限制">
              <TextArea
                rows={2}
                value={draft.filter.include.join('\n')}
                placeholder="招生"
                onChange={(e) =>
                  patch((d) => void (d.filter.include = e.target.value.split('\n').filter(Boolean)))
                }
              />
            </Field>
            <Field label="排除关键词" hint="每行一条；命中即丢弃该条">
              <TextArea
                rows={2}
                value={draft.filter.exclude.join('\n')}
                placeholder="招聘
广告"
                onChange={(e) =>
                  patch((d) => void (d.filter.exclude = e.target.value.split('\n').filter(Boolean)))
                }
              />
            </Field>
            <Checkbox
              label="按正则匹配（而不是纯文本包含）"
              checked={draft.filter.regex}
              onChange={(e) => patch((d) => void (d.filter.regex = e.target.checked))}
            />
          </section>

          {!isPage ? (
            <section className="mt-3 space-y-2 rounded border border-line-soft bg-panel/40 px-3 py-2">
              <Switch
                label="跟进详情页取正文（写入 RSS 正文）"
                checked={draft.article.enabled}
                onChange={(next) => patch((d) => void (d.article.enabled = next))}
              />
              {draft.article.enabled ? (
                <>
                  <Field label="正文选择器" hint="留空则用 readability 自动识别">
                    <Input
                      value={draft.article.content}
                      placeholder="css:#zoom / xpath://div[@class='article']"
                      onChange={(e) => patch((d) => void (d.article.content = e.target.value))}
                    />
                  </Field>
                  <div className="flex items-end gap-1.5">
                    <Field label="详情页地址（预览用）" className="min-w-0 flex-1">
                      <Input
                        value={articleLink}
                        placeholder="https://example.com/news/1"
                        className="font-mono text-[12px]"
                        onChange={(e) => setArticleLink(e.target.value)}
                      />
                    </Field>
                    <Button
                      variant="default"
                      onClick={() => void openDetail()}
                      disabled={busy === 'detail' || busy === 'article' || !articleLink.trim()}
                      title="抓取这条详情页，在右侧预览并点选正文容器"
                    >
                      {busy === 'detail' ? (
                        <Loader2 size={13} className="animate-spin" />
                      ) : (
                        <Crosshair size={13} />
                      )}
                      预览正文
                    </Button>
                  </div>
                  <Switch
                    label="选择器未命中时回退 readability"
                    checked={draft.article.readability}
                    onChange={(next) => patch((d) => void (d.article.readability = next))}
                  />
                  <details className="rounded border border-line-soft/70 px-2 py-1.5">
                    <summary className="cursor-pointer text-[11px] text-muted">
                      正文字段（可选）
                    </summary>
                    <div className="mt-1.5 space-y-1.5">
                      <Field label="正文标题选择器" hint="留空沿用条目标题">
                        <Input
                          value={draft.article.title}
                          className="font-mono text-[12px]"
                          onChange={(e) => patch((d) => void (d.article.title = e.target.value))}
                        />
                      </Field>
                      <Field label="发布时间选择器" hint="留空沿用列表里的日期">
                        <Input
                          value={draft.article.date}
                          className="font-mono text-[12px]"
                          onChange={(e) => patch((d) => void (d.article.date = e.target.value))}
                        />
                      </Field>
                      <Field label="正文截断字符数" hint="0 表示不截断；只影响纯文本">
                        <Input
                          type="number"
                          value={draft.article.max_chars}
                          onChange={(e) =>
                            patch((d) => void (d.article.max_chars = Number(e.target.value)))
                          }
                        />
                      </Field>
                    </div>
                  </details>
                </>
              ) : null}
            </section>
          ) : null}

          <section className="mt-3 space-y-2 rounded border border-line-soft bg-panel/40 px-3 py-2">
            <p className="text-[11px] uppercase tracking-wide text-muted">通知</p>
            <div className="flex flex-wrap gap-2">
              {targetList.length === 0 ? (
                <span className="text-[11px] text-faint">
                  还没有通道，去「通知通道」页添加 ntfy / Telegram / Bark 等地址
                </span>
              ) : (
                targetList.map((t) => (
                  <Checkbox
                    key={t.id}
                    label={t.name}
                    checked={draft.policy.notify.target_ids.includes(t.id)}
                    onChange={(e) =>
                      patch((d) => {
                        const ids = new Set(d.policy.notify.target_ids)
                        if (e.target.checked) ids.add(t.id)
                        else ids.delete(t.id)
                        d.policy.notify.target_ids = [...ids]
                      })
                    }
                  />
                ))
              )}
            </div>
            <div className="flex flex-wrap gap-x-3 gap-y-1.5 border-t border-line-soft/60 pt-2">
              {TRIGGERS.map((tr) => (
                <Checkbox
                  key={tr.value}
                  label={tr.label}
                  checked={draft.policy.notify.triggers.includes(tr.value)}
                  onChange={(e) =>
                    patch((d) => {
                      const set = new Set(d.policy.notify.triggers)
                      if (e.target.checked) set.add(tr.value)
                      else set.delete(tr.value)
                      d.policy.notify.triggers = [...set]
                    })
                  }
                />
              ))}
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Field label="发送时机" hint="batch 30s 合并；digest 每日汇总">
                <Select
                  value={draft.policy.notify.timing}
                  onChange={(e) => patch((d) => void (d.policy.notify.timing = e.target.value))}
                >
                  <option value="immediate">立即</option>
                  <option value="batch">合并（30 秒窗口）</option>
                  <option value="digest">每日汇总</option>
                </Select>
              </Field>
              <Field label="内容详略">
                <Select
                  value={draft.policy.notify.detail}
                  onChange={(e) => patch((d) => void (d.policy.notify.detail = e.target.value))}
                >
                  <option value="none">只发标题</option>
                  <option value="summary">标题 + 摘要</option>
                  <option value="full">标题 + 全文</option>
                </Select>
              </Field>
            </div>
            {draft.policy.notify.timing === 'digest' ? (
              <Field label="汇总时刻" hint="每天这个时间把当天变化合成一条消息">
                <Input
                  type="time"
                  value={draft.policy.notify.digest_at}
                  onChange={(e) => patch((d) => void (d.policy.notify.digest_at = e.target.value))}
                />
              </Field>
            ) : null}
            <div className="grid grid-cols-2 gap-2">
              <Field label="静默时段" hint="如 22:00-08:00，故障通知不受限">
                <Input
                  value={draft.policy.notify.quiet_hours}
                  placeholder="22:00-08:00"
                  className="font-mono text-[12px]"
                  onChange={(e) =>
                    patch((d) => void (d.policy.notify.quiet_hours = e.target.value))
                  }
                />
              </Field>
              <Field label="空跑告警阈值" hint="连续多少次 0 命中算静默失效">
                <Input
                  type="number"
                  value={draft.policy.notify.silent_after}
                  onChange={(e) =>
                    patch((d) => void (d.policy.notify.silent_after = Number(e.target.value)))
                  }
                />
              </Field>
            </div>
            <Field label="同源节流（秒）" hint="窗口内同源只发一次；故障与恢复不受此限制">
              <Input
                type="number"
                value={draft.policy.notify.throttle_sec}
                onChange={(e) =>
                  patch((d) => void (d.policy.notify.throttle_sec = Number(e.target.value)))
                }
              />
            </Field>
          </section>
        </div>

        {/* 右：预览 / 抽取结果 */}
        <div className="flex min-h-0 flex-col">
          <div className="flex items-center gap-2 border-b border-line-soft px-4 py-2">
            {pickTarget === 'item' && (isHTML || isPage || isJSON) ? (
              <>
                <Button
                  onClick={() => void fetchPreview()}
                  disabled={busy === 'preview' || !draft.url}
                >
                  {busy === 'preview' ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <RefreshCw size={13} />
                  )}
                  抓取预览
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => void runProbeExtract()}
                  disabled={!sid || busy === 'extract'}
                  title={
                    previewKind === 'json'
                      ? '用当前 item 路径在接口响应上抽取'
                      : '用当前选择器在预览 DOM 上抽取'
                  }
                >
                  <Crosshair size={13} /> 按当前选择器抽取
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => scrollToFirstMatch()}
                  disabled={!sid || !draft.extract.item}
                  title={
                    previewKind === 'json'
                      ? '滚动到 item 路径所在的行'
                      : '把第一条命中滚进视野（移动端页面列表常在首屏之外）'
                  }
                >
                  <Locate size={13} /> 定位首条
                </Button>
                {previewKind === 'json' ? null : (
                  <Select
                    value={viewWidth}
                    onChange={(e) => setViewWidth(e.target.value as typeof viewWidth)}
                    className="w-[86px]"
                    title="预览宽度：移动端 H5 用「手机」才不会被桌面布局挤走内容"
                  >
                    <option value="auto">全宽</option>
                    <option value="390">手机</option>
                    <option value="768">平板</option>
                  </Select>
                )}
                {previewKind === 'json' ? null : (
                  <Button
                    variant="ghost"
                    onClick={() => setExpand((v) => !v)}
                    title={
                      expand
                        ? '收起预览，回到抽取结果'
                        : '放大预览：移动端页面顶部有吸顶筛选栏，面板太矮会挡住列表'
                    }
                    className="shrink-0"
                  >
                    {expand ? <Minimize2 size={13} /> : <Maximize2 size={13} />}
                    {expand ? '收起' : '放大'}
                  </Button>
                )}
              </>
            ) : null}
            {pickTarget === 'article' && detail ? (
              <>
                <Button variant="ghost" onClick={() => setPickTarget('item')}>
                  <ArrowLeft size={13} /> 返回列表预览
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => void runArticlePreview()}
                  disabled={busy === 'article'}
                  title="用当前正文选择器在详情页上重新识别"
                >
                  {busy === 'article' ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <RefreshCw size={13} />
                  )}
                  重新识别正文
                </Button>
              </>
            ) : null}
            <span className="ml-auto truncate font-mono text-[11px] text-faint">
              {pickTarget === 'article' && detail
                ? `${detail.title || '（无标题）'} · 详情页`
                : previewMeta
                  ? `${previewMeta.title || '（无标题）'} · ${(previewMeta.bytes / 1024).toFixed(1)} KB`
                  : ''}
              {pickTarget === 'item' && sid ? ` · sid ${sid.slice(0, 6)}` : ''}
            </span>
          </div>

          <div className="relative flex min-h-0 flex-1 justify-center overflow-hidden bg-void">
            {previewWarn.length ? (
              <div className="absolute inset-x-0 top-0 z-10 border-b border-warn/40 bg-warn/10 px-4 py-1.5 text-[11px] leading-relaxed text-warn">
                {previewWarn.map((w) => (
                  <div key={w}>{w}</div>
                ))}
              </div>
            ) : null}
            {frameSid ? (
              <iframe
                key={frameSid}
                ref={frameRef}
                title="preview"
                src={`/api/preview/${frameSid}`}
                sandbox="allow-scripts"
                className="h-full border-0 bg-white"
                style={{ width: viewWidth === 'auto' ? '100%' : `${viewWidth}px` }}
              />
            ) : (
              <div className="flex h-full items-center justify-center px-6 text-center text-[12px] text-faint">
                {isHTML || isPage
                  ? '填好地址后点「抓取预览」，在页面里点击要监控的条目（悬停高亮，↑ 选父级，↓ 选子级）'
                  : draft.kind === 'json'
                    ? '填好 API 地址后点「抓取预览」，在右侧接口响应里点选字段路径（点 ▸ 展开层级）'
                    : draft.article.enabled
                      ? '在左侧「正文」区填一条详情页地址，点「预览正文」，就能在右侧点选正文容器'
                      : '订阅转发不需要框选：填好地址后点右上「试跑」，能解析就行'}
              </div>
            )}
          </div>

          <div
            className={cn(
              'flex min-h-[200px] shrink-0 flex-col overflow-auto border-t border-line-soft',
              expand ? 'hidden' : 'h-[40%]',
            )}
          >
            {pickTarget === 'article' ? (
              <ArticlePreviewPane
                view={articleView}
                error={articleError}
                picked={articlePicked}
                busy={busy}
              />
            ) : (
              <>
                {suggestion ? (
                  <div className="border-b border-line-soft px-4 py-2">
                    <div className="mb-1 flex items-center gap-2">
                      <span className="text-[11px] uppercase tracking-wide text-muted">
                        候选选择器
                      </span>
                      <Button size="sm" variant="ghost" onClick={applyFieldGuesses}>
                        套用字段推断
                      </Button>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {suggestion.candidates.map((c) => (
                        <button
                          key={`${c.kind}-${c.xpath}`}
                          onClick={() => applyCandidate(c)}
                          className={cn(
                            'rounded border px-2 py-1 text-left font-mono text-[11px]',
                            draft.extract.item === c.xpath
                              ? 'border-accent/60 bg-accent/10 text-accent'
                              : 'border-line text-muted hover:border-muted',
                          )}
                          title={c.xpath}
                        >
                          {c.label} · 命中 {c.count}
                        </button>
                      ))}
                    </div>
                    {suggestion.fields.length ? (
                      <div className="mt-1.5 flex flex-wrap gap-1.5 text-[11px] text-faint">
                        字段推断：
                        {suggestion.fields.map((f) => (
                          <span key={f.name} className="font-mono">
                            {f.name}=
                            <span className="text-muted">{f.text.slice(0, 16) || f.sel}</span>
                          </span>
                        ))}
                      </div>
                    ) : null}
                  </div>
                ) : null}

                <div className="flex items-center justify-between px-4 py-1.5">
                  <p className="text-[11px] uppercase tracking-wide text-muted">
                    抽取结果 <span className="font-mono text-faint">{rows.length}</span>
                  </p>
                  <div className="flex gap-1">
                    {probeWarn.map((w) => (
                      <Badge key={w} tone="warn">
                        {w}
                      </Badge>
                    ))}
                  </div>
                </div>
                {rows.length === 0 ? (
                  <p className="px-4 pb-4 text-[12px] text-faint">
                    还没有结果。先框选条目，或点「按当前选择器抽取」。
                  </p>
                ) : (
                  <table className="w-full border-collapse text-[12px]">
                    <thead className="text-[11px] text-faint">
                      <tr className="border-b border-line-soft">
                        <th className="px-4 py-1 text-left font-normal">标题</th>
                        <th className="px-2 py-1 text-left font-normal">日期</th>
                        <th className="px-2 py-1 text-left font-normal">链接</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.slice(0, 30).map((it, i) => (
                        <tr key={i} className="border-b border-line-soft/50">
                          <td className="max-w-[280px] truncate px-4 py-1">{it.title || '—'}</td>
                          <td className="whitespace-nowrap px-2 py-1 font-mono text-[11px] text-muted">
                            {String(it.date || '—').slice(0, 19)}
                          </td>
                          <td className="max-w-[320px] truncate px-2 py-1 font-mono text-[11px] text-faint">
                            {it.link}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </>
            )}
          </div>
        </div>
      </div>
      {editing ? (
        <div className="border-t border-line-soft px-5 py-2">
          <Button
            variant="danger"
            size="sm"
            onClick={async () => {
              if (!confirm('删除这个监控源？')) return
              await api.deleteSource(Number(id))
              qc.invalidateQueries({ queryKey: ['sources'] })
              nav('/')
            }}
          >
            <Trash2 size={12} /> 删除此源
          </Button>
        </div>
      ) : null}
    </div>
  )
}

function ArticlePreviewPane({
  view,
  error,
  picked,
  busy,
}: {
  view: ArticlePreview | null
  error: string
  picked: string
  busy: string
}) {
  return (
    <>
      <div className="flex items-center justify-between gap-2 border-b border-line-soft px-4 py-1.5">
        <p className="text-[11px] uppercase tracking-wide text-muted">正文预览</p>
        <div className="flex min-w-0 items-center gap-2">
          {view?.method ? (
            <Badge tone="accent">
              {view.method === 'selector' ? '选择器命中' : 'readability 兜底'}
            </Badge>
          ) : null}
          {picked ? (
            <span className="truncate font-mono text-[10px] text-faint" title={picked}>
              {picked}
            </span>
          ) : null}
        </div>
      </div>
      <div className="px-4 py-2">
        {error ? (
          <p className="text-[12px] text-danger">{error}</p>
        ) : view ? (
          <>
            {view.title ? <p className="text-[13px] font-medium">{view.title}</p> : null}
            {view.date ? (
              <p className="mb-2 font-mono text-[11px] text-faint">{view.date}</p>
            ) : null}
            <div
              className="article-preview mt-1"
              // 正文已在 article.Extract 里用 bluemonday 净化
              dangerouslySetInnerHTML={{ __html: view.content_html }}
            />
          </>
        ) : (
          <p className="text-[12px] text-faint">
            {busy === 'article'
              ? '正在识别正文…'
              : '在右侧详情页点选正文容器，或点上方「重新识别正文」'}
          </p>
        )}
      </div>
    </>
  )
}
