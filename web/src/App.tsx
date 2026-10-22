import { useQuery, useQueryClient } from '@tanstack/react-query'
import { NavLink, Outlet } from 'react-router'
import { useEffect, useState } from 'react'
import { Activity, Bell, ListTree, Radar, Settings } from 'lucide-react'
import { api } from './api'
import { Button, Input, cn } from './components/ui'
import { startLive, useLive } from './live'

function Login({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex h-full items-center justify-center px-6">
      <form
        onSubmit={submit}
        className="w-full max-w-xs rounded-lg border border-line-soft bg-panel p-5"
      >
        <div className="mb-4 flex items-baseline gap-2">
          <span className="text-[18px] tracking-[0.2em]">司寤</span>
          <span className="font-mono text-[11px] text-faint">siwu</span>
        </div>
        <Input
          type="password"
          autoFocus
          placeholder="访问口令"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error ? <p className="mt-2 text-[12px] text-danger">{error}</p> : null}
        <Button
          type="submit"
          variant="primary"
          className="mt-3 w-full justify-center"
          disabled={busy}
        >
          {busy ? '验证中…' : '进入'}
        </Button>
      </form>
    </div>
  )
}

const nav = [
  { to: '/', label: '监控源', icon: Radar, end: true },
  { to: '/runs', label: '运行记录', icon: Activity },
  { to: '/targets', label: '通知通道', icon: Bell },
  { to: '/settings', label: '设置', icon: Settings },
]

export default function App() {
  const qc = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const live = useLive()

  // 登录后订阅服务端事件：运行结束就刷新列表、图表与运行记录
  useEffect(() => {
    if (!me.data?.authenticated) return
    startLive((e) => {
      if (e.type !== 'run.finish' && e.type !== 'notify.ok' && e.type !== 'notify.fail') return
      qc.invalidateQueries({ queryKey: ['sources'] })
      qc.invalidateQueries({ queryKey: ['stats'] })
      if (e.type === 'run.finish') {
        qc.invalidateQueries({ queryKey: ['runs'] })
        if (e.source_id) {
          qc.invalidateQueries({ queryKey: ['source', e.source_id] })
          qc.invalidateQueries({ queryKey: ['entries', e.source_id] })
        }
      }
    })
  }, [me.data?.authenticated, qc])

  if (me.isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-[12px] text-faint">加载中…</div>
    )
  }
  if (me.data?.auth_required && !me.data.authenticated) {
    return <Login onDone={() => qc.invalidateQueries({ queryKey: ['me'] })} />
  }

  return (
    <div className="flex h-full">
      <aside className="flex w-[196px] shrink-0 flex-col border-r border-line-soft bg-panel/60">
        <div className="flex items-baseline gap-2 px-4 pt-4 pb-3">
          <span className="text-[17px] tracking-[0.22em]">司寤</span>
          <span className="font-mono text-[10px] text-faint">siwu</span>
        </div>
        <nav className="flex flex-col gap-0.5 px-2">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  'flex h-8 items-center gap-2 rounded px-2 text-[13px]',
                  isActive
                    ? 'bg-accent/12 text-accent'
                    : 'text-muted hover:bg-panel-2 hover:text-ink',
                )
              }
            >
              <item.icon size={14} strokeWidth={1.6} />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto px-4 py-3 text-[11px] leading-relaxed text-faint">
          <p className="flex items-center gap-1.5">
            <span
              className={cn(
                'inline-block size-1.5 rounded-full',
                live.connected ? 'bg-accent' : 'bg-muted/50',
              )}
              title={live.connected ? '实时事件已连接' : '实时事件未连接'}
            />
            <span className="font-mono">{live.connected ? 'live' : 'offline'}</span>
            {live.running.size > 0 ? (
              <span className="text-accent">· {live.running.size} 个源在跑</span>
            ) : null}
          </p>
          <p className="mt-1 font-mono">司夜守更 · 见变即告</p>
          {me.data?.auth_required ? (
            <button
              className="mt-1 text-faint underline-offset-2 hover:text-muted hover:underline"
              onClick={async () => {
                await api.logout()
                qc.invalidateQueries({ queryKey: ['me'] })
              }}
            >
              退出登录
            </button>
          ) : null}
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-y-auto">
        <Outlet />
      </main>
    </div>
  )
}

export function PageHead({
  title,
  desc,
  actions,
}: {
  title: string
  desc?: string
  actions?: React.ReactNode
}) {
  return (
    <header className="flex items-end justify-between gap-4 border-b border-line-soft px-5 py-3">
      <div>
        <h1 className="text-[15px] font-medium">{title}</h1>
        {desc ? <p className="mt-0.5 text-[11px] text-faint">{desc}</p> : null}
      </div>
      <div className="flex items-center gap-2">{actions}</div>
    </header>
  )
}

export { ListTree }
