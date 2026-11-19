import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * { post } provide the controlled request fixtures used by
 * admin.providers.duplicate tests to inspect their observable results.
 */
const { post } = vi.hoisted(() => ({
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { post },
}))

import { duplicate } from '@/api/admin/providers'

/**
 * admin.providers.duplicate.spec.ts checks provider-copy operation keys before
 * and after ambiguous and deterministic failures. Request mocks expose reuse
 * across timeouts, conflicts, and reloads while verifying that deterministic
 * 4xx client rejections clear both caches so subsequent attempts generate a
 * fresh operation key.
 */

describe('admin provider duplicate API', () => {
  beforeEach(() => {
    sessionStorage.clear()
    post.mockReset()
    post.mockResolvedValue({ data: { id: 43, name: 'primary (Copy)' } })
    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue(
      '11111111-1111-4111-8111-111111111111',
    )
  })

  /**
   * A provider copy uses a deterministic UUID for its first request. The
   * idempotency header contains the source provider identifier and the returned
   * copy is unwrapped.
   */
  it('sends a stable idempotency key with the duplicate request', async () => {
    const provider = await duplicate(42)

    expect(post).toHaveBeenCalledWith('/admin/providers/42/duplicate', undefined, {
      headers: {
        'Idempotency-Key': 'provider-duplicate-42-11111111-1111-4111-8111-111111111111',
      },
    })
    expect(provider).toEqual({ id: 43, name: 'primary (Copy)' })
  })

  /**
   * A provider copy times out before a retry succeeds. The pending key uses the
   * TokenRouter sessionStorage namespace and both requests carry the same
   * operation header.
   */
  it('reuses the operation key after an ambiguous failed request', async () => {
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(duplicate(99)).rejects.toThrow('network timeout')

    const storedKey = sessionStorage.getItem('tokenrouter:admin:provider-duplicate:99')
    expect(storedKey).toBe('provider-duplicate-99-11111111-1111-4111-8111-111111111111')
    expect(sessionStorage.getItem('sub2api:admin:provider-duplicate:99')).toBeNull()

    post.mockResolvedValueOnce({ data: { id: 100, name: 'retry (Copy)' } })
    await duplicate(99)

    expect(post).toHaveBeenCalledTimes(2)
    const firstHeaders = post.mock.calls[0][2].headers
    const secondHeaders = post.mock.calls[1][2].headers
    expect(secondHeaders).toEqual(firstHeaders)
  })

  /**
   * Deterministic 400 and 404 rejections prove the server refused the duplicate
   * attempt, so both memory and sessionStorage keys clear before the next call
   * generates a fresh idempotency key.
   */
  it('clears the operation key after deterministic 4xx client rejections', async () => {
    post.mockRejectedValueOnce({ status: 400, message: 'shadow provider cannot be duplicated' })
    await expect(duplicate(55)).rejects.toMatchObject({ status: 400 })
    expect(sessionStorage.getItem('tokenrouter:admin:provider-duplicate:55')).toBeNull()

    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue(
      '22222222-2222-4222-8222-222222222222',
    )
    post.mockResolvedValueOnce({ data: { id: 56, name: 'fresh (Copy)' } })
    await duplicate(55)

    expect(post.mock.calls[0][2].headers['Idempotency-Key']).toBe(
      'provider-duplicate-55-11111111-1111-4111-8111-111111111111',
    )
    expect(post.mock.calls[1][2].headers['Idempotency-Key']).toBe(
      'provider-duplicate-55-22222222-2222-4222-8222-222222222222',
    )
  })

  /**
   * Rate-limited (429), conflict (409), and server (503) rejections remain
   * retryable under the original operation key.
   */
  it.each([409, 429, 503])(
    'retains the operation key after retryable status %i',
    async (status) => {
      post.mockRejectedValueOnce({ status, message: 'retryable' })
      await expect(duplicate(66)).rejects.toMatchObject({ status })
      expect(sessionStorage.getItem('tokenrouter:admin:provider-duplicate:66')).toBe(
        'provider-duplicate-66-11111111-1111-4111-8111-111111111111',
      )

      vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue(
        '33333333-3333-4333-8333-333333333333',
      )
      post.mockResolvedValueOnce({ data: { id: 67, name: 'retried (Copy)' } })
      await duplicate(66)
      expect(post.mock.calls[1][2].headers['Idempotency-Key']).toBe(
        'provider-duplicate-66-11111111-1111-4111-8111-111111111111',
      )
    },
  )

  /**
   * A failed provider copy is followed by a module reload. The request reuses
   * the persisted operation header and successful completion removes its
   * sessionStorage entry.
   */
  it('reuses the operation key after a page reload', async () => {
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(duplicate(77)).rejects.toThrow('network timeout')
    const firstHeaders = post.mock.calls[0][2].headers

    vi.resetModules()
    post.mockResolvedValueOnce({ data: { id: 78, name: 'reload (Copy)' } })
    const { duplicate: duplicateAfterReload } = await import('@/api/admin/providers')
    await duplicateAfterReload(77)

    expect(post).toHaveBeenCalledTimes(2)
    expect(post.mock.calls[1][2].headers).toEqual(firstHeaders)
    expect(sessionStorage.length).toBe(0)
  })
})
