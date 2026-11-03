import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getAvailableMarketplaceModels, getMarketplaceModels } from '../marketplace'

// marketplace.spec.ts verifies the separate public and authenticated catalog URLs.
// The shared client is replaced at its HTTP boundary, preserving query options
// and rejection behavior while preventing tests from accessing a real account.

// Controlled HTTP responses make scope selection and request failures directly observable.
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get } }))

describe('marketplace API scopes', () => {
  beforeEach(() => {
    get.mockReset()
  })

  // Public and personal callers use distinct URLs with capacity aggregation disabled.
  // Both return server data directly so authorization remains owned by the backend.
  it('keeps public and personal catalog endpoints separate', async () => {
    const data = [{ id: 1, is_exclusive: true }]
    get.mockResolvedValue({ data })
    expect(await getAvailableMarketplaceModels()).toBe(data)
    expect(get).toHaveBeenLastCalledWith('/marketplace/models/available', {
      params: { include_capacity: false },
    })
    await getMarketplaceModels()
    expect(get).toHaveBeenLastCalledWith('/marketplace/models', {
      params: { include_capacity: false },
    })
  })

  // An authorization error must reach the page's error state without a public retry.
  // Silent fallback would make a permission outage appear to revoke private groups.
  it('propagates personal query failures without public fallback', async () => {
    const failure = new Error('Permissions unavailable')
    get.mockRejectedValue(failure)
    await expect(getAvailableMarketplaceModels()).rejects.toBe(failure)
    expect(get).toHaveBeenCalledOnce()
  })
})
