import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { useState } from 'react'
import { Copy, Download, Loader2, Play, Plus, Settings2 } from 'lucide-react'
import { api } from '../api'
import { PageHead } from '../App'
import { Badge, Button, Empty, Panel, fmtTime, relTime } from '../components/ui'
import { useLive } from '../live'
import type { SourceView } from '../types'

const kindLabel: Record<string, string> = { html: '列表', json: '接口', page: '快照', rss: '转发' }

function statusOf(s: SourceView, running: boolean) {
  if (running) return { text: '运行中', tone: 'accent' as const }
  if (!s.enabled) return { text: '已停用', tone: 'neutral' as const }
  if (s.consecutive_failures > 0)
    return { text: `连续失败 ${s.consecutive_failures}`, tone: 'danger' as const }
  if (!s.last_run_at) return { text: '待首跑', tone: 'warn' as const }
  return { text: '正常', tone: 'accent' as const }
}

// 统计块固定三行（标题 / 数值 / 备注），备注为空时也占位，
// 否则带备注和不带备注的块上下错位，数字会「高高低低」。
function Metric({ label, value, hint }: { label: string; value: number; hint?: string }) {
  return (
    <div className="min-w-[92px]">
      <p className="text-[11px] leading-4 text-faint">{label}</p>
      <p className="mt-0.5 font-mono text-[17px] leading-none tabular-nums">{value}</p>
      <p className="mt-1 text-[10px] leading-3 text-faint">{hint ?? '\u00A0'}</p>
    </div>
  )
}

export default function SourcesPage() {
  const qc = useQueryClient()
  const [notice, setNotice] = useState('')
  const sources = useQuery({ queryKey: ['sources'], queryFn: api.sources })
  const stats = useQuery({ queryKey: ['stats'], queryFn: api.stats })
  const live = useLive()

  const run = useMutation({
    mutationFn: (id: number) => api.runSource(id),
    onSuccess: (res) => {
      setNotice(
        `${res.run.status === 'error' ? '运行失败' : '运行完成'}：命中 ${res.run.items_found} 条，新增 ${res.run.items_new} 条${
          res.updated ? `，更新 ${res.updated} 条` : ''
        }${res.run.duration_ms ? `，${res.run.duration_ms}ms` : ''}${
          res.error ? `（${res.error}）` : ''
        }`,
      )
      qc.invalidateQueries({ queryKey: ['sources'] })
      qc.invalidateQueries({ queryKey: ['runs'] })
      qc.invalidateQueries({ queryKey: ['stats'] })
    },
    onError: (err) => setNotice(err instanceof Error ? err.message : '运行失败'),
  })

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteSource(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sources'] })
      qc.invalidateQueries({ queryKey: ['stats'] })
    },
  })

  return (
    <div className="flex h-full flex-col">
      <PageHead
        title="监控源"
        desc="每个源定时抓取一次，产出 RSS / Atom / JSON Feed，并按策略推送通知"
        actions={
          <>
            <a href={api.opmlUrl} download="siwu.opml">
              <Button variant="ghost" title="把所有订阅导出为 OPML，可导入任意阅读器">
                <Download size={13} /> OPML
              </Button>
            </a>
            <Link to="/sources/new">
              <Button variant="primary">
                <Plus size={14} /> 新建监控源
              </Button>
            </Link>
          </>
        }
      />
      {notice ? (
        <div className="border-b border-line-soft bg-panel/40 px-5 py-2 text-[12px] text-muted">
          {notice}
        </div>
      ) : null}
      {stats.data ? (
        <div className="flex flex-wrap items-start gap-x-8 gap-y-2 border-b border-line-soft px-5 py-3">
          <Metric label="监控源" value={stats.data.sources} hint={`${stats.data.enabled} 个启用`} />
          <Metric label="条目总数" value={stats.data.entries} />
          <Metric label="今日新增" value={stats.data.new_today} />
          <Metric
            label="24h 异常"
            value={stats.data.errors_24h}
            hint={stats.data.errors_24h > 0 ? '检查运行记录' : '无'}
          />
          <Metric
            label="24h 空跑"
            value={stats.data.empty_runs_24h}
            hint={stats.data.empty_runs_24h > 0 ? '选择器可能失效' : '无'}
          />
          <div className="ml-auto self-center text-right text-[11px] text-faint">
            <p>上次运行 {relTime(stats.data.last_run_at)}</p>
            <p>下次运行 {relTime(stats.data.next_run_at)}</p>
          </div>
        </div>
      ) : null}
      <div className="flex-1 overflow-auto">
        {sources.isLoading ? (
          <Empty>加载中…</Empty>
        ) : sources.isError ? (
          <Empty>读取失败：{sources.error.message}</Empty>
        ) : !sources.data?.length ? (
          <Empty>
            还没有监控源。点右上角「新建监控源」，填网址后可以直接在页面里框选要监控的区域。
          </Empty>
        ) : (
          <table className="w-full border-collapse text-[12px]">
            <thead className="sticky top-0 z-10 bg-void text-[11px] text-faint">
              <tr className="border-b border-line-soft">
                <th className="px-5 py-2 text-left font-normal">名称</th>
                <th className="px-2 py-2 text-left font-normal">形态</th>
                <th className="px-2 py-2 text-left font-normal">通道</th>
                <th className="px-2 py-2 text-right font-normal">条目</th>
                <th className="px-2 py-2 text-left font-normal">上次运行</th>
                <th className="px-2 py-2 text-left font-normal">下次</th>
                <th className="px-2 py-2 text-left font-normal">状态</th>
                <th className="px-5 py-2 text-right font-normal">操作</th>
              </tr>
            </thead>
            <tbody>
              {sources.data.map((s) => {
                const busy = live.running.has(s.id)
                const st = statusOf(s, busy)
                return (
                  <tr key={s.id} className="border-b border-line-soft/60 hover:bg-panel/50">
                    <td className="px-5 py-2">
                      <Link to={`/sources/${s.id}`} className="text-ink hover:text-accent">
                        {s.name}
                      </Link>
                      <div
                        className="truncate font-mono text-[11px] text-faint"
                        style={{ maxWidth: 420 }}
                      >
                        {s.url}
                      </div>
                    </td>
                    <td className="px-2 py-2 text-muted">{kindLabel[s.kind] ?? s.kind}</td>
                    <td className="px-2 py-2">
                      <Badge tone={s.engine === 'browser' ? 'warn' : 'neutral'}>
                        {s.engine === 'browser' ? '浏览器' : 'HTTP'}
                      </Badge>
                    </td>
                    <td className="px-2 py-2 text-right font-mono tabular-nums">{s.entry_count}</td>
                    <td className="px-2 py-2 text-muted" title={fmtTime(s.last_run_at)}>
                      {relTime(s.last_run_at)}
                    </td>
                    <td className="px-2 py-2 text-muted" title={fmtTime(s.next_run_at)}>
                      {s.enabled ? relTime(s.next_run_at) : '—'}
                    </td>
                    <td className="px-2 py-2">
                      <Badge tone={st.tone}>{st.text}</Badge>
                    </td>
                    <td className="px-5 py-2">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => run.mutate(s.id)}
                          disabled={run.isPending || busy}
                        >
                          {busy ? (
                            <Loader2 size={12} className="animate-spin" />
                          ) : (
                            <Play size={12} />
                          )}{' '}
                          {busy ? '运行中' : '运行'}
                        </Button>
                        <Link to={`/sources/${s.id}/entries`}>
                          <Button size="sm" variant="ghost">
                            条目
                          </Button>
                        </Link>
                        <Button
                          size="sm"
                          variant="ghost"
                          title="复制 RSS 地址"
                          onClick={() => {
                            void navigator.clipboard?.writeText(s.feed_rss)
                            setNotice(`已复制：${s.feed_rss}`)
                          }}
                        >
                          <Copy size={12} />
                        </Button>
                        <Link to={`/sources/${s.id}`}>
                          <Button size="sm" variant="ghost" title="编辑">
                            <Settings2 size={12} />
                          </Button>
                        </Link>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          onClick={() => {
                            if (confirm(`删除「${s.name}」及其全部条目？`)) remove.mutate(s.id)
                          }}
                        >
                          删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
      <Panel className="mx-5 my-3 px-3 py-2 text-[11px] text-faint">
        订阅地址形如 <code className="font-mono text-muted">/f/&lt;slug&gt;.xml</code>
        ，把域名换成你的部署地址即可。 Atom 用 <code className="font-mono text-muted">.atom</code>
        ，JSON Feed 用 <code className="font-mono text-muted">.json</code>。
      </Panel>
    </div>
  )
}
