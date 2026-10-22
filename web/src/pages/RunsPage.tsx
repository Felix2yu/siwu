import { useQuery } from '@tanstack/react-query'
import { Fragment, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { api } from '../api'
import { PageHead } from '../App'
import { Badge, Empty, Select, cn, fmtTime } from '../components/ui'
import { useLive } from '../live'

const tone = (status: string) => {
  switch (status) {
    case 'ok':
      return 'accent' as const
    case 'empty':
      return 'warn' as const
    case 'error':
      return 'danger' as const
    case 'running':
      return 'neutral' as const
    default:
      return 'neutral' as const
  }
}

const statusLabel: Record<string, string> = {
  ok: '正常',
  empty: '0 命中',
  error: '失败',
  running: '运行中',
  interrupted: '已中断',
}

const triggerLabel: Record<string, string> = {
  manual: '手动',
  test: '试跑',
  schedule: '定时',
}

export default function RunsPage() {
  const [sourceId, setSourceId] = useState(0)
  const [open, setOpen] = useState<number | null>(null)
  const live = useLive()
  const runs = useQuery({ queryKey: ['runs', sourceId], queryFn: () => api.runs(sourceId, 100) })
  const sources = useQuery({ queryKey: ['sources'], queryFn: api.sources })
  const nameOf = (id: number) => sources.data?.find((s) => s.id === id)?.name ?? `#${id}`

  return (
    <div className="flex h-full flex-col">
      <PageHead
        title="运行记录"
        desc="每次抓取的命中数、新增数与耗时；连续 0 命中说明选择器可能已失效"
        actions={
          <Select
            value={String(sourceId)}
            className="w-52"
            onChange={(e) => setSourceId(Number(e.target.value))}
          >
            <option value="0">全部监控源</option>
            {(sources.data ?? []).map((s) => (
              <option key={s.id} value={String(s.id)}>
                {s.name}
              </option>
            ))}
          </Select>
        }
      />
      {live.running.size > 0 ? (
        <div className="border-b border-line-soft bg-accent/8 px-5 py-1.5 text-[11px] text-accent">
          正在运行 {live.running.size} 个源：{[...live.running].map(nameOf).join('、')}
        </div>
      ) : null}
      <div className="flex-1 overflow-auto">
        {runs.isLoading ? (
          <Empty>加载中…</Empty>
        ) : !runs.data?.length ? (
          <Empty>还没有运行记录。</Empty>
        ) : (
          <table className="w-full border-collapse text-[12px]">
            <thead className="sticky top-0 z-10 bg-void text-[11px] text-faint">
              <tr className="border-b border-line-soft">
                <th className="w-6 px-2 py-2" />
                <th className="px-2 py-2 text-left font-normal">时间</th>
                <th className="px-2 py-2 text-left font-normal">监控源</th>
                <th className="px-2 py-2 text-left font-normal">触发</th>
                <th className="px-2 py-2 text-left font-normal">状态</th>
                <th className="px-2 py-2 text-right font-normal">命中</th>
                <th className="px-2 py-2 text-right font-normal">新增</th>
                <th className="px-2 py-2 text-right font-normal">更新</th>
                <th className="px-2 py-2 text-right font-normal">耗时</th>
                <th className="px-5 py-2 text-left font-normal">异常 / 警告</th>
              </tr>
            </thead>
            <tbody>
              {runs.data.map((r) => {
                const hasDetail = Boolean(r.error) || Boolean(r.warnings?.length)
                const expanded = open === r.id
                return (
                  <Fragment key={r.id}>
                    <tr
                      className={cn(
                        'border-b border-line-soft/60 hover:bg-panel/40',
                        hasDetail && 'cursor-pointer',
                      )}
                      onClick={() => hasDetail && setOpen(expanded ? null : r.id)}
                    >
                      <td className="px-2 py-2 text-faint">
                        {hasDetail ? (
                          expanded ? (
                            <ChevronDown size={12} />
                          ) : (
                            <ChevronRight size={12} />
                          )
                        ) : null}
                      </td>
                      <td className="whitespace-nowrap px-2 py-2 font-mono text-[11px] text-muted">
                        {fmtTime(r.started_at)}
                      </td>
                      <td className="px-2 py-2">{nameOf(r.source_id)}</td>
                      <td className="px-2 py-2 text-muted">
                        {triggerLabel[r.trigger] ?? r.trigger ?? '—'}
                      </td>
                      <td className="px-2 py-2">
                        <Badge tone={tone(r.status)}>{statusLabel[r.status] ?? r.status}</Badge>
                      </td>
                      <td className="px-2 py-2 text-right font-mono tabular-nums">
                        {r.items_found}
                      </td>
                      <td className="px-2 py-2 text-right font-mono tabular-nums">{r.items_new}</td>
                      <td className="px-2 py-2 text-right font-mono tabular-nums text-muted">
                        {r.items_updated || 0}
                      </td>
                      <td className="px-2 py-2 text-right font-mono tabular-nums text-muted">
                        {r.duration_ms}ms
                      </td>
                      <td className="max-w-[360px] truncate px-5 py-2 text-[11px] text-muted">
                        {r.error ? <span className="text-danger">{r.error}</span> : null}
                        {!r.error && r.warnings?.length ? r.warnings[0] : null}
                        {!r.error && !r.warnings?.length ? '—' : null}
                        {!r.error && (r.warnings?.length ?? 0) > 1
                          ? ` …等 ${r.warnings?.length} 条`
                          : null}
                      </td>
                    </tr>
                    {expanded ? (
                      <tr className="bg-panel/40">
                        <td />
                        <td colSpan={9} className="px-2 pb-3 text-[11px] leading-relaxed">
                          {r.error ? <p className="mb-1 text-danger">错误：{r.error}</p> : null}
                          {(r.warnings ?? []).map((w, i) => (
                            <p key={i} className="text-muted">
                              · {w}
                            </p>
                          ))}
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
