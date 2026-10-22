import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Send, Trash2 } from 'lucide-react'
import { api } from '../api'
import { PageHead } from '../App'
import { Badge, Button, Empty, Field, Input, Panel, PanelHead } from '../components/ui'

const EXAMPLES = [
  { label: 'ntfy', url: 'ntfys://ntfy.example.com/siwu' },
  { label: 'Telegram', url: 'tgram://bot_token/chat_id' },
  { label: 'Bark (iOS)', url: 'bark://host/key' },
  { label: '企业微信群机器人', url: 'wecom://key' },
  { label: '飞书', url: 'feishu://token' },
  { label: '通用 JSON webhook', url: 'json://host/path' },
]

export default function TargetsPage() {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [message, setMessage] = useState('')
  const targets = useQuery({ queryKey: ['targets'], queryFn: api.targets })
  const schemas = useQuery({ queryKey: ['schemas'], queryFn: api.schemas })

  const create = useMutation({
    mutationFn: () => api.createTarget({ name, url }),
    onSuccess: () => {
      setName('')
      setUrl('')
      setMessage('已添加')
      qc.invalidateQueries({ queryKey: ['targets'] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '添加失败'),
  })

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteTarget(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['targets'] }),
  })

  const update = useMutation({
    mutationFn: (t: { id: number; name: string; enabled: boolean }) => api.updateTarget(t),
    onSuccess: () => {
      setMessage('已更新')
      qc.invalidateQueries({ queryKey: ['targets'] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '更新失败'),
  })

  const flush = useMutation({
    mutationFn: () => api.flushNotify(),
    onSuccess: () => setMessage('已冲刷合并/汇总队列'),
    onError: (err) => setMessage(err instanceof Error ? err.message : '冲刷失败'),
  })

  const test = useMutation({
    mutationFn: (body: { url?: string; ids?: number[] }) => api.testTargets(body),
    onSuccess: (res) => setMessage(`测试消息已发往 ${res.sent} 个目标`),
    onError: (err) => setMessage(err instanceof Error ? err.message : '发送失败'),
  })

  return (
    <div className="flex h-full flex-col">
      <PageHead
        title="通知通道"
        desc={`通过 apprise-go 发送，当前构建支持 ${schemas.data?.schemas.length ?? '…'} 个协议；地址里的 token 会加密落库`}
        actions={
          <Button variant="ghost" onClick={() => flush.mutate()} disabled={flush.isPending}>
            <Send size={13} /> 冲刷合并队列
          </Button>
        }
      />
      {message ? (
        <div className="border-b border-line-soft bg-panel/40 px-5 py-1.5 text-[12px] text-muted">
          {message}
        </div>
      ) : null}
      <div className="grid flex-1 grid-cols-[minmax(420px,1fr)_360px] gap-4 overflow-auto p-5">
        <div>
          {targets.isLoading ? (
            <Empty>加载中…</Empty>
          ) : !targets.data?.length ? (
            <Empty>还没有通知通道。右侧填一个地址，先点「测试」确认能收到，再保存。</Empty>
          ) : (
            <table className="w-full border-collapse text-[12px]">
              <thead className="text-[11px] text-faint">
                <tr className="border-b border-line-soft">
                  <th className="py-2 text-left font-normal">名称</th>
                  <th className="py-2 text-left font-normal">地址</th>
                  <th className="py-2 text-right font-normal">操作</th>
                </tr>
              </thead>
              <tbody>
                {targets.data.map((t) => (
                  <tr key={t.id} className="border-b border-line-soft/60">
                    <td className="py-2">
                      <button
                        className="text-left hover:text-accent"
                        title="点击改名"
                        onClick={() => {
                          const v = prompt('通道名称', t.name)
                          if (v && v.trim() && v !== t.name) {
                            update.mutate({ id: t.id, name: v.trim(), enabled: t.enabled })
                          }
                        }}
                      >
                        {t.name}
                      </button>
                      {t.enabled ? null : (
                        <span className="ml-2">
                          <Badge tone="neutral">已停用</Badge>
                        </span>
                      )}
                    </td>
                    <td className="py-2 font-mono text-[11px] text-faint">{t.masked}</td>
                    <td className="py-2 text-right">
                      <label
                        className="mr-1 inline-flex cursor-pointer items-center gap-1 text-[11px] text-faint"
                        title={t.enabled ? '停用后不再发往此通道' : '启用'}
                      >
                        <input
                          type="checkbox"
                          className="size-3.5 accent-[var(--color-accent)]"
                          checked={t.enabled}
                          onChange={(e) =>
                            update.mutate({ id: t.id, name: t.name, enabled: e.target.checked })
                          }
                        />
                      </label>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => test.mutate({ ids: [t.id] })}
                      >
                        <Send size={12} /> 测试
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger"
                        onClick={() => remove.mutate(t.id)}
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

        <Panel className="h-fit">
          <PanelHead title="添加通道" desc="地址来自 Apprise，粘贴后先测试再保存" />
          <div className="space-y-2 p-3">
            <Field label="名称">
              <Input
                value={name}
                placeholder="我的手机"
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label="Apprise 地址">
              <Input
                value={url}
                placeholder="ntfys://ntfy.example.com/siwu"
                onChange={(e) => setUrl(e.target.value)}
              />
            </Field>
            <div className="flex flex-wrap gap-1">
              {EXAMPLES.map((ex) => (
                <button
                  key={ex.label}
                  className="rounded border border-line px-1.5 py-0.5 text-[11px] text-muted hover:border-muted hover:text-ink"
                  onClick={() => setUrl(ex.url)}
                  type="button"
                >
                  {ex.label}
                </button>
              ))}
            </div>
            <div className="flex items-center gap-2 pt-1">
              <Button
                variant="ghost"
                onClick={() => url && test.mutate({ url })}
                disabled={!url || test.isPending}
              >
                <Send size={13} /> 测试
              </Button>
              <Button
                variant="primary"
                onClick={() => create.mutate()}
                disabled={!name || !url || create.isPending}
              >
                保存
              </Button>
            </div>
            <div className="pt-1 text-[11px] text-faint">
              <p className="mb-1">可用协议（部分）：</p>
              <div className="flex max-h-32 flex-wrap gap-1 overflow-auto">
                {(schemas.data?.schemas ?? []).slice(0, 80).map((s) => (
                  <Badge key={s}>{s}</Badge>
                ))}
              </div>
            </div>
          </div>
        </Panel>
      </div>
    </div>
  )
}
