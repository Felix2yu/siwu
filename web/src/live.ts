import { useSyncExternalStore } from 'react'
import type { LiveEvent } from './types'

interface LiveState {
  connected: boolean
  /** 正在运行的源 id */
  running: ReadonlySet<number>
  last: LiveEvent | null
  recent: LiveEvent[]
}

let state: LiveState = { connected: false, running: new Set(), last: null, recent: [] }
const listeners = new Set<() => void>()
let started = false

function emit(patch: Partial<LiveState>) {
  state = { ...state, ...patch }
  for (const fn of listeners) fn()
}

function subscribe(fn: () => void) {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

function getSnapshot(): LiveState {
  return state
}

/**
 * 建立 SSE 连接。EventSource 自带断线重连，这里只维护一份可订阅的进程内状态，
 * 让「哪些源正在跑」和「最近发生了什么」在多个页面之间共享。
 */
export function startLive(onEvent?: (e: LiveEvent) => void) {
  if (started || typeof window === 'undefined' || typeof EventSource === 'undefined') return
  started = true
  const es = new EventSource('/api/events')
  es.onopen = () => emit({ connected: true })
  es.onerror = () => emit({ connected: false })

  const handle = (e: MessageEvent) => {
    let data: LiveEvent
    try {
      data = JSON.parse(e.data) as LiveEvent
    } catch {
      return
    }
    const running = new Set(state.running)
    if (data.source_id) {
      if (data.type === 'run.start') running.add(data.source_id)
      if (data.type === 'run.finish') running.delete(data.source_id)
    }
    emit({
      running,
      last: data,
      recent: [data, ...state.recent].slice(0, 60),
    })
    onEvent?.(data)
  }
  for (const type of ['run.start', 'run.finish', 'notify.ok', 'notify.fail']) {
    es.addEventListener(type, handle as EventListener)
  }
}

export function useLive(): LiveState {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
