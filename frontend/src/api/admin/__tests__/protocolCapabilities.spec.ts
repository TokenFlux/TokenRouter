import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { loadProtocolCatalog, protocolCatalog, protocolCatalogError, protocolCatalogLoading } from '../protocolCapabilities'
import fixture from '@/__tests__/fixtures/protocol-catalog.json'

beforeEach(() => {
  protocolCatalog.value = null
  protocolCatalogError.value = false
  protocolCatalogLoading.value = false
})
afterEach(() => vi.restoreAllMocks())

// 从真实空缓存开始验证请求合并与失败恢复，不能由全局夹具跳过加载流程。
describe('协议目录加载', () => {
  it('并发调用共享请求，加载成功后复用缓存', async () => {
    let resolve!: (value: { data: typeof fixture }) => void
    const request = vi.spyOn(apiClient, 'get').mockImplementation(() => new Promise(done => { resolve = done }))
    const first = loadProtocolCatalog()
    const second = loadProtocolCatalog()
    expect(first).toBe(second)
    expect(protocolCatalogLoading.value).toBe(true)
    expect(request).toHaveBeenCalledTimes(1)
    resolve({ data: structuredClone(fixture) })
    await first
    expect(protocolCatalogLoading.value).toBe(false)
    expect(protocolCatalogError.value).toBe(false)
    expect(await loadProtocolCatalog()).toEqual(fixture)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('失败不缓存空目录，重试清除错误并恢复数据', async () => {
    const request = vi.spyOn(apiClient, 'get').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: structuredClone(fixture) })
    await expect(loadProtocolCatalog()).rejects.toThrow('offline')
    expect(protocolCatalog.value).toBeNull()
    expect(protocolCatalogError.value).toBe(true)
    expect(protocolCatalogLoading.value).toBe(false)
    const retry = loadProtocolCatalog()
    expect(protocolCatalogError.value).toBe(false)
    expect(protocolCatalogLoading.value).toBe(true)
    await retry
    expect(protocolCatalog.value).toEqual(fixture)
    expect(request).toHaveBeenCalledTimes(2)
  })
})
