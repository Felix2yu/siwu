import { describe, expect, it } from 'vitest'
import { blankSource } from './types'

describe('blankSource', () => {
  it('默认走 HTTP 通道、列表型，并且是启用的', () => {
    const s = blankSource()
    expect(s.kind).toBe('html')
    expect(s.engine).toBe('http')
    expect(s.enabled).toBe(true)
    expect(s.request.timeout_sec).toBe(20)
  })

  it('通知默认「立即 + 摘要」，并盯住新增/失败/恢复/连续空跑', () => {
    const n = blankSource().policy.notify
    expect(n.timing).toBe('immediate')
    expect(n.detail).toBe('summary')
    expect(n.triggers).toContain('new_entry')
    expect(n.triggers).toContain('failure')
    expect(n.triggers).toContain('recovery')
    expect(n.triggers).toContain('silent_empty')
    // 默认不绑任何通道：没配目标时通知层应直接跳过，而不是报错
    expect(n.target_ids).toEqual([])
  })

  it('调度默认用固定间隔，cron 留空且保留期不限', () => {
    const p = blankSource().policy
    expect(p.interval_sec).toBe(3600)
    expect(p.cron).toBe('')
    expect(p.retention_days).toBe(0)
    expect(p.min_changed_lines).toBe(0)
  })

  it('每次调用返回独立对象（就地编辑不会串味）', () => {
    const a = blankSource()
    const b = blankSource()
    a.policy.notify.triggers.push('custom')
    a.extract.fields.title = {
      sel: 'css:a',
      attr: '',
      absolute: false,
      fallback: [],
      optional: false,
      template: '',
      transforms: [],
    }
    expect(b.policy.notify.triggers).not.toContain('custom')
    expect(b.extract.fields).toEqual({})
  })
})
