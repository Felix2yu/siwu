import { afterEach, describe, expect, it, vi } from 'vitest'
import { cn, fmtTime, relTime } from './ui'

// 固定「现在」，让相对时间断言与机器时钟无关
const NOW = new Date('2026-10-22T08:00:00+08:00').getTime()

afterEach(() => {
  vi.useRealTimers()
})

function atNow() {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  const sec = Math.floor(NOW / 1000)
  return (delta: number) => sec + delta
}

describe('cn', () => {
  it('拼接并剔除假值', () => {
    expect(cn('a', 'b')).toBe('a b')
    expect(cn('a', false, null, undefined, 'b')).toBe('a b')
    expect(cn()).toBe('')
  })
})

describe('fmtTime', () => {
  it('空值给出占位符', () => {
    expect(fmtTime(undefined)).toBe('—')
    expect(fmtTime(0)).toBe('—')
  })

  it('输出 YYYY-MM-DD HH:mm', () => {
    // 不断言具体时刻：fmtTime 用本地时区，断言形状才对 CI（UTC）与本机都成立
    expect(fmtTime(1761091200)).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })
})

describe('relTime', () => {
  it('空值给出占位符', () => {
    expect(relTime(undefined)).toBe('—')
    expect(relTime(0)).toBe('—')
  })

  it('过去时间按分钟/小时/天分档', () => {
    const ts = atNow()
    expect(relTime(ts(-5))).toBe('刚刚')
    expect(relTime(ts(-90))).toBe('1 分钟前')
    expect(relTime(ts(-59 * 60))).toBe('59 分钟前')
    expect(relTime(ts(-2 * 3600))).toBe('2 小时前')
    expect(relTime(ts(-23 * 3600))).toBe('23 小时前')
    expect(relTime(ts(-3 * 86400))).toBe('3 天前')
  })

  // 回归用例：列表页「下次运行」是未来时间，曾被算成「刚刚」
  it('未来时间按分钟/小时/天分档，且不落到「刚刚」', () => {
    const ts = atNow()
    expect(relTime(ts(45))).toBe('即将')
    expect(relTime(ts(20 * 60))).toBe('20 分钟后')
    expect(relTime(ts(2 * 3600))).toBe('2 小时后')
    expect(relTime(ts(3 * 86400))).toBe('3 天后')
    expect(relTime(ts(30 * 60))).not.toBe('刚刚')
  })

  it('30 秒内的小幅时钟漂移仍算「刚刚」', () => {
    const ts = atNow()
    expect(relTime(ts(10))).toBe('刚刚')
  })
})
