import { afterEach, describe, expect, it, vi } from 'vitest'

describe('api url helpers', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.resetModules()
  })

  it('builds api urls without duplicating api prefix', async () => {
    const { buildApiUrl } = await import('@/api/url')

    expect(buildApiUrl('/pages/intro')).toBe('/api/v1/pages/intro')
    expect(buildApiUrl('/api/v1/pages/intro')).toBe('/api/v1/pages/intro')
  })

  it('builds api urls from configured absolute api base', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://api.example.com/proxy/api/v1/')
    const { buildApiUrl } = await import('@/api/url')

    expect(buildApiUrl('/pages/intro')).toBe('https://api.example.com/proxy/api/v1/pages/intro')
    expect(buildApiUrl('/auth/refresh')).toBe('https://api.example.com/proxy/api/v1/auth/refresh')
  })

  it('builds gateway urls from configured api origin', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://api.example.com/api/v1')
    const { buildGatewayUrl } = await import('@/api/url')

    expect(buildGatewayUrl('/v1/usage')).toBe('https://api.example.com/v1/usage')
  })
})
