import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router'
import { useState } from 'react'
import { Copy, Trash2 } from 'lucide-react'
import { api } from '../api'
import { PageHead } from '../App'
import { Button, Empty, fmtTime } from '../components/ui'

const PAGE_SIZE = 50

export default function EntriesPage() {
  const { id } = useParams()
  const sourceId = Number(id)
  const qc = useQueryClient()
  const [open, setOpen] = useState<number | null>(null)
  const [offset, setOffset] = useState(0)

  const source = useQuery({ queryKey: ['source', sourceId], queryFn: () => api.source(sourceId) })
  const entries = useQuery({
    queryKey: ['entries', sourceId, offset],
    queryFn: () => api.entries(sourceId, PAGE_SIZE, offset),
  })
  const remove = useMutation({
    mutationFn: (entryId: number) => api.deleteEntry(entryId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['entries', sourceId] })
      qc.invalidateQueries({ queryKey: ['source', sourceId] })
    },
  })

  return (
    <div className="flex h-full flex-col">
      <PageHead
        title={`条目 · ${source.data?.name ?? ''}`}
        desc={`共 ${entries.data?.total ?? 0} 条；RSS 里按发布时间倒序输出`}
        actions={
          <>
            <Button
              variant="ghost"
              onClick={() => {
                if (source.data) void navigator.clipboard?.writeText(source.data.feed_rss)
              }}
            >
              <Copy size={13} /> 复制订阅地址
            </Button>
            <Link to={`/sources/${sourceId}`}>
              <Button variant="ghost">编辑配置</Button>
            </Link>
          </>
        }
      />
      <div className="flex-1 overflow-auto">
        {entries.isLoading ? (
          <Empty>加载中…</Empty>
        ) : !entries.data?.items?.length ? (
          <Empty>还没有条目。回监控源点一次「运行」。</Empty>
        ) : (
          <table className="w-full border-collapse text-[12px]">
            <thead className="sticky top-0 bg-void text-[11px] text-faint">
              <tr className="border-b border-line-soft">
                <th className="px-5 py-2 text-left font-normal">标题</th>
                <th className="px-2 py-2 text-left font-normal">发布时间</th>
                <th className="px-2 py-2 text-left font-normal">分类</th>
                <th className="px-2 py-2 text-left font-normal">已通知</th>
                <th className="px-5 py-2 text-right font-normal">操作</th>
              </tr>
            </thead>
            <tbody>
              {entries.data.items.map((e) => (
                <tr key={e.id} className="border-b border-line-soft/60 align-top hover:bg-panel/40">
                  <td className="px-5 py-2">
                    <button
                      className="text-left hover:text-accent"
                      onClick={() => setOpen(open === e.id ? null : e.id)}
                    >
                      {e.title || '（无标题）'}
                    </button>
                    {open === e.id ? (
                      <div className="mt-1 max-w-3xl space-y-1 text-[11px] text-muted">
                        <a
                          href={e.link}
                          target="_blank"
                          rel="noreferrer"
                          className="block truncate font-mono text-faint hover:text-accent"
                        >
                          {e.link}
                        </a>
                        <div
                          className="max-h-56 overflow-auto rounded border border-line-soft bg-panel p-2 text-[12px] leading-relaxed"
                          // 正文已在服务端用 bluemonday 净化
                          dangerouslySetInnerHTML={{
                            __html: e.content_html || e.summary || '（无正文）',
                          }}
                        />
                      </div>
                    ) : null}
                  </td>
                  <td className="whitespace-nowrap px-2 py-2 font-mono text-[11px] text-muted">
                    {fmtTime(e.published_at || e.first_seen_at)}
                  </td>
                  <td className="px-2 py-2 text-muted">{e.category || '—'}</td>
                  <td className="whitespace-nowrap px-2 py-2 font-mono text-[11px] text-faint">
                    {e.notified_at ? fmtTime(e.notified_at) : '—'}
                  </td>
                  <td className="px-5 py-2 text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-danger"
                      onClick={() => remove.mutate(e.id)}
                    >
                      <Trash2 size={12} />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {(entries.data?.total ?? 0) > PAGE_SIZE ? (
        <div className="flex items-center justify-between border-t border-line-soft px-5 py-2 text-[11px] text-muted">
          <span className="font-mono">
            {offset + 1}–{Math.min(offset + PAGE_SIZE, entries.data?.total ?? 0)} /{' '}
            {entries.data?.total ?? 0}
          </span>
          <div className="flex gap-1">
            <Button
              size="sm"
              variant="ghost"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
            >
              上一页
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={offset + PAGE_SIZE >= (entries.data?.total ?? 0)}
              onClick={() => setOffset(offset + PAGE_SIZE)}
            >
              下一页
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  )
}
