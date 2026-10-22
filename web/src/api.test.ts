import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'

type Call = { path: string; init?: RequestInit }

function stubFetch(body: string, status = 200) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      calls.push({ path, init })
      return new Response(body, { status })
    }),
  )
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('api.req', () => {
  it('解析 JSON 响应', async () => {
    stubFetch(JSON.stringify({ authenticated: true, auth_required: false }))
    await expect(api.me()).resolves.toEqual({ authenticated: true, auth_required: false })
  })

  it('把后端 {error} 转成 Error 消息', async () => {
    stubFetch(JSON.stringify({ error: '口令不正确' }), 401)
    await expect(api.me()).rejects.toThrow('口令不正确')
  })

  it('无响应体时按状态码报错，而不是抛 JSON 解析错', async () => {
    stubFetch('', 500)
    await expect(api.sources()).rejects.toThrow('请求失败（HTTP 500）')
  })

  it('非 JSON 响应原样返回（如代理返回的纯文本）', async () => {
    stubFetch('Bad Gateway', 200)
    await expect(api.transforms()).resolves.toBe('Bad Gateway')
  })

  it('带 body 的请求自动加 Content-Type', async () => {
    const calls = stubFetch(JSON.stringify({ ok: true }))
    await api.login('secret')
    expect(calls[0]?.init?.method).toBe('POST')
    expect(calls[0]?.init?.headers).toEqual({ 'Content-Type': 'application/json' })
    expect(calls[0]?.init?.credentials).toBe('same-origin')
  })

  it('GET 请求不设 Content-Type', async () => {
    const calls = stubFetch('[]')
    await api.sources()
    expect(calls[0]?.init?.headers).toBeUndefined()
  })
})
