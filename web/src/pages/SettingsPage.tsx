import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { ReactNode } from 'react'
import { Monitor, Moon, Sun } from 'lucide-react'
import { api } from '../api'
import { PageHead } from '../App'
import { Button, Empty, Field, Input, Panel, PanelHead, Select, Switch, cn } from '../components/ui'
import { loadThemeMode, saveThemeMode } from '../theme'
import type { ThemeMode } from '../theme'
import type { SourceDefaults, SystemInfo } from '../types'

const THEMES = [
  { value: 'system', label: '跟随系统', icon: Monitor },
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
] as const

export default function SettingsPage() {
  const [theme, setTheme] = useState<ThemeMode>(loadThemeMode)
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings })

  function chooseTheme(next: ThemeMode) {
    setTheme(next)
    saveThemeMode(next)
  }

  return (
    <div className="flex h-full flex-col">
      <PageHead title="设置" desc="外观即时生效，只影响当前浏览器；默认值作用于整个实例" />
      <div className="flex-1 overflow-auto">
        <div className="mx-auto w-full max-w-3xl space-y-4 px-5 py-5">
          <Panel>
            <PanelHead title="外观" desc="浅色、深色，或跟随系统自动切换" />
            <div className="flex flex-wrap items-center gap-1.5 px-3 py-3">
              {THEMES.map((item) => {
                const Icon = item.icon
                return (
                  <button
                    key={item.value}
                    type="button"
                    aria-pressed={theme === item.value}
                    onClick={() => chooseTheme(item.value)}
                    className={cn(
                      'inline-flex h-8 items-center gap-1.5 rounded border px-2.5 text-[12px] transition-colors',
                      theme === item.value
                        ? 'border-accent/60 bg-accent/12 text-accent'
                        : 'border-line text-muted hover:bg-panel-2 hover:text-ink',
                    )}
                  >
                    <Icon size={13} /> {item.label}
                  </button>
                )
              })}
            </div>
          </Panel>

          {settings.isLoading ? (
            <Panel>
              <Empty>加载中…</Empty>
            </Panel>
          ) : settings.isError ? (
            <Panel>
              <Empty>读取设置失败：{settings.error.message}</Empty>
            </Panel>
          ) : settings.data ? (
            <>
              <DefaultsPanel
                key={JSON.stringify(settings.data.defaults)}
                initial={settings.data.defaults}
              />
              <SystemPanel info={settings.data.system} />
            </>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function DefaultsPanel({ initial }: { initial: SourceDefaults }) {
  const qc = useQueryClient()
  const [form, setForm] = useState(initial)
  const [message, setMessage] = useState('')
  const save = useMutation({
    mutationFn: () => api.saveSettings(form),
    onSuccess: (res) => {
      setForm(res.defaults)
      setMessage('已保存，之后新建的源会套用')
      qc.invalidateQueries({ queryKey: ['settings'] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '保存失败'),
  })

  return (
    <Panel>
      <PanelHead title="新建源默认值" desc="只影响之后创建的源，已有源保持原配置" />
      <div className="space-y-2 px-3 py-3">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Field label="间隔（秒）">
            <Input
              type="number"
              value={form.interval_sec}
              onChange={(e) => setForm({ ...form, interval_sec: Number(e.target.value) })}
            />
          </Field>
          <Field label="输出条数">
            <Input
              type="number"
              value={form.max_items}
              onChange={(e) => setForm({ ...form, max_items: Number(e.target.value) })}
            />
          </Field>
          <Field label="保留天数" hint="0 表示永久">
            <Input
              type="number"
              value={form.retention_days}
              onChange={(e) => setForm({ ...form, retention_days: Number(e.target.value) })}
            />
          </Field>
          <Field label="去重策略">
            <Select
              value={form.update_mode}
              onChange={(e) => setForm({ ...form, update_mode: e.target.value })}
            >
              <option value="update">变更时更新</option>
              <option value="ignore">只看新增</option>
              <option value="archive">变更留版本</option>
            </Select>
          </Field>
        </div>
        <div className="flex items-center justify-between gap-3 border-t border-line-soft pt-2">
          <Switch
            label="新建源默认跟进详情页取正文"
            checked={form.article_enabled}
            onChange={(next) => setForm({ ...form, article_enabled: next })}
          />
          <Button
            variant="primary"
            size="sm"
            onClick={() => save.mutate()}
            disabled={save.isPending}
          >
            {save.isPending ? '保存中…' : '保存默认值'}
          </Button>
        </div>
        {message ? <p className="text-[11px] text-faint">{message}</p> : null}
      </div>
    </Panel>
  )
}

function SystemPanel({ info }: { info: SystemInfo }) {
  const rows: Array<{ label: string; value: ReactNode }> = []
  const mono = (v: string) => <span className="font-mono text-[11px]">{v}</span>
  if (info.version) rows.push({ label: '版本', value: mono(info.version) })
  if (info.base_url) rows.push({ label: '对外地址', value: mono(info.base_url) })
  if (info.timezone) rows.push({ label: '时区', value: mono(info.timezone) })
  if (info.workers > 0) rows.push({ label: '抓取并发', value: mono(String(info.workers)) })
  if (info.data_dir) rows.push({ label: '数据目录', value: mono(info.data_dir) })
  rows.push({ label: '访问口令', value: info.auth_required ? '已启用' : '未设置' })
  rows.push({ label: '订阅令牌', value: info.feed_token_required ? '已启用' : '未设置' })
  rows.push({ label: '内网抓取', value: info.allow_private ? '已允许' : '已拦截' })
  if (info.browser_ws) rows.push({ label: '外置浏览器', value: mono(info.browser_ws) })
  if (info.chrome_path) rows.push({ label: '本机 Chrome', value: mono(info.chrome_path) })

  return (
    <Panel>
      <PanelHead title="系统" desc="来自启动参数与环境变量，修改后需重启 siwu" />
      <dl className="grid grid-cols-[88px_minmax(0,1fr)] gap-x-3 gap-y-1.5 px-3 py-3 text-[12px]">
        {rows.map((row) => (
          <div key={row.label} className="contents">
            <dt className="text-faint">{row.label}</dt>
            <dd className="break-all text-muted">{row.value}</dd>
          </div>
        ))}
      </dl>
    </Panel>
  )
}
