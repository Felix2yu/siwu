import { afterEach, describe, expect, it, vi } from 'vitest'
import { startLive } from './live'
import type { LiveEvent } from './types'

class FakeEventSource {
  static instances: FakeEventSource[] = []
  listeners = new Map<string, ((e: MessageEvent) => void)[]>()
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: EventListener) {
    const list = this.listeners.get(type) ?? []
    list.push(fn as (e: MessageEvent) => void)
    this.listeners.set(type, list)
  }

  emit(type: string, data: string) {
    for (const fn of this.listeners.get(type) ?? []) fn({ data } as MessageEvent)
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('startLive', () => {
  it('建立单条 SSE 连接，按事件名分发并跳过坏数据', () => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
    // startLive 有 typeof window 守卫（SSR 下不该建连接），node 环境要先补上
    vi.stubGlobal('window', {})

    const got: LiveEvent[] = []
    startLive((e) => got.push(e))

    // 只允许一条连接：多页共享同一份进程内状态
    startLive((e) => got.push(e))
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0]?.url).toBe('/api/events')

    const es = FakeEventSource.instances[0]
    expect(es).toBeDefined()
    if (!es) return

    // onopen / onerror 只切连接状态，不应抛出
    expect(() => es.onopen?.()).not.toThrow()
    expect(() => es.onerror?.()).not.toThrow()

    es.emit('run.start', JSON.stringify({ type: 'run.start', at: 1, source_id: 7 }))
    es.emit('run.finish', JSON.stringify({ type: 'run.finish', at: 2, source_id: 7, found: 12 }))
    expect(got).toHaveLength(2)
    expect(got[0]?.source_id).toBe(7)
    expect(got[1]?.found).toBe(12)

    // 半截/非法 JSON 不能让整个流挂掉
    es.emit('run.start', 'not json')
    expect(got).toHaveLength(2)
  })
})
