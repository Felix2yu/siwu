import { describe, expect, it } from 'vitest'
import {
  DEFAULT_INTERVAL_SEC,
  applySourceDefaults,
  blankSource,
  intervalMinutes,
  intervalSeconds,
} from './types'

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
    expect(p.interval_sec).toBe(DEFAULT_INTERVAL_SEC)
    expect(p.interval_sec % 60).toBe(0)
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

describe('applySourceDefaults', () => {
  it('只覆盖设置页暴露的五项', () => {
    const s = applySourceDefaults(blankSource(), {
      interval_sec: 600,
      max_items: 20,
      retention_days: 7,
      update_mode: 'archive',
      article_enabled: true,
    })
    expect(s.policy.interval_sec).toBe(600)
    expect(s.policy.max_items).toBe(20)
    expect(s.policy.retention_days).toBe(7)
    expect(s.policy.update_mode).toBe('archive')
    expect(s.article.enabled).toBe(true)
    expect(s.request.timeout_sec).toBe(20)
    expect(s.policy.notify.detail).toBe('summary')
  })

  it('没有设置时保持 blankSource 原值', () => {
    const s = applySourceDefaults(blankSource(), undefined)
    expect(s.policy.interval_sec).toBe(DEFAULT_INTERVAL_SEC)
    expect(s.article.enabled).toBe(false)
  })
})

describe('间隔按分钟换算', () => {
  it('秒转分钟四舍五入，非法值退回默认', () => {
    expect(intervalMinutes(1800)).toBe(30)
    expect(intervalMinutes(600)).toBe(10)
    expect(intervalMinutes(0)).toBe(DEFAULT_INTERVAL_SEC / 60)
    expect(intervalMinutes(Number.NaN)).toBe(DEFAULT_INTERVAL_SEC / 60)
  })

  it('分钟转秒且不低于 1 分钟', () => {
    expect(intervalSeconds(30)).toBe(1800)
    expect(intervalSeconds(1)).toBe(60)
    expect(intervalSeconds(0)).toBe(DEFAULT_INTERVAL_SEC)
    expect(intervalSeconds(-3)).toBe(DEFAULT_INTERVAL_SEC)
    expect(intervalSeconds(2.4)).toBe(120)
  })
})
