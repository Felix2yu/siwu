import type { ReactNode } from 'react'

export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}

export function Button({
  children,
  variant = 'default',
  size = 'md',
  className,
  ...rest
}: {
  children: ReactNode
  variant?: 'default' | 'primary' | 'ghost' | 'danger'
  size?: 'sm' | 'md'
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const base =
    'inline-flex items-center gap-1.5 whitespace-nowrap rounded border transition-colors disabled:opacity-40 disabled:cursor-not-allowed'
  const sizes = size === 'sm' ? 'h-7 px-2 text-[12px]' : 'h-8 px-3'
  const variants = {
    default: 'border-line bg-panel-2 text-ink hover:border-muted hover:bg-line-soft',
    primary: 'border-accent/60 bg-accent/15 text-accent hover:bg-accent/25',
    ghost: 'border-transparent bg-transparent text-muted hover:bg-panel-2 hover:text-ink',
    danger: 'border-danger/50 bg-danger/10 text-danger hover:bg-danger/20',
  }[variant]
  return (
    <button className={cn(base, sizes, variants, className)} {...rest}>
      {children}
    </button>
  )
}

export function Input({ className, ...rest }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        'h-8 w-full rounded border border-line bg-panel px-2 text-[13px] outline-none',
        'placeholder:text-faint focus:border-accent/70 focus:ring-1 focus:ring-accent/25',
        className,
      )}
      {...rest}
    />
  )
}

export function TextArea({
  className,
  ...rest
}: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        'w-full rounded border border-line bg-panel px-2 py-1.5 font-mono text-[12px] outline-none',
        'placeholder:text-faint focus:border-accent/70 focus:ring-1 focus:ring-accent/25',
        className,
      )}
      {...rest}
    />
  )
}

export function Select({
  className,
  children,
  ...rest
}: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'h-8 w-full rounded border border-line bg-panel px-2 text-[13px] outline-none',
        'focus:border-accent/70 focus:ring-1 focus:ring-accent/25',
        className,
      )}
      {...rest}
    >
      {children}
    </select>
  )
}

export function Field({
  label,
  hint,
  children,
  className,
}: {
  label: string
  hint?: string
  children: ReactNode
  className?: string
}) {
  return (
    <label className={cn('flex flex-col gap-1', className)}>
      <span className="text-[11px] uppercase tracking-wide text-muted">{label}</span>
      {children}
      {hint ? <span className="text-[11px] text-faint">{hint}</span> : null}
    </label>
  )
}

export function Panel({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <section className={cn('rounded-lg border border-line-soft bg-panel', className)}>
      {children}
    </section>
  )
}

export function PanelHead({
  title,
  desc,
  right,
}: {
  title: string
  desc?: string
  right?: ReactNode
}) {
  return (
    <header className="flex items-start justify-between gap-3 border-b border-line-soft px-3 py-2">
      <div className="min-w-0">
        <h2 className="text-[13px] font-medium">{title}</h2>
        {desc ? <p className="mt-0.5 text-[11px] text-faint">{desc}</p> : null}
      </div>
      {right}
    </header>
  )
}

export function Badge({
  children,
  tone = 'neutral',
}: {
  children: ReactNode
  tone?: 'neutral' | 'accent' | 'warn' | 'danger'
}) {
  const tones = {
    neutral: 'border-line text-muted',
    accent: 'border-accent/40 bg-accent/10 text-accent',
    warn: 'border-warn/40 bg-warn/10 text-warn',
    danger: 'border-danger/40 bg-danger/10 text-danger',
  }[tone]
  return (
    <span
      className={cn(
        'inline-flex items-center rounded border px-1.5 py-0.5 text-[11px] leading-none',
        tones,
      )}
    >
      {children}
    </span>
  )
}

export function Checkbox({
  label,
  ...rest
}: { label: string } & React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-[12px] text-muted">
      <input type="checkbox" className="size-3.5 accent-[var(--c-accent)]" {...rest} />
      {label}
    </label>
  )
}

export function Switch({
  label,
  checked,
  onChange,
  disabled,
  title,
}: {
  label: string
  checked: boolean
  onChange: (next: boolean) => void
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      title={title}
      onClick={() => onChange(!checked)}
      className={cn(
        'inline-flex items-center gap-2 text-[12px] transition-opacity disabled:opacity-40',
        checked ? 'text-ink' : 'text-muted',
      )}
    >
      <span
        aria-hidden
        className={cn(
          'relative inline-flex h-4 w-7 shrink-0 items-center rounded-full border transition-colors',
          checked ? 'border-accent/70 bg-accent/80' : 'border-line bg-panel-2',
        )}
      >
        <span
          className={cn(
            'absolute size-3 rounded-full bg-panel shadow-sm transition-all',
            checked ? 'left-3.5' : 'left-0.5',
          )}
        />
      </span>
      {label}
    </button>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="px-3 py-8 text-center text-[12px] text-faint">{children}</div>
}

export function fmtTime(ts?: number): string {
  if (!ts) return '—'
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function relTime(ts?: number): string {
  if (!ts) return '—'
  const diff = Date.now() / 1000 - ts
  // 未来时间（下次运行）不能落到「刚刚」分支里
  if (diff < -30) {
    const ahead = -diff
    if (ahead < 90) return '即将'
    if (ahead < 3600) return `${Math.round(ahead / 60)} 分钟后`
    if (ahead < 86400) return `${Math.round(ahead / 3600)} 小时后`
    return `${Math.round(ahead / 86400)} 天后`
  }
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}
