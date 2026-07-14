import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post
  }
}))

import { consumeCodexInviteReset, syncFromCrs } from '@/api/admin/accounts'

describe('admin accounts API', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('uses a dedicated 180 second timeout for CRS synchronization', async () => {
    const payload = {
      base_url: 'https://crs.example.com',
      username: 'admin',
      password: 'secret',
      sync_proxies: true,
      selected_account_ids: ['crs-1']
    }
    const response = {
      created: 1,
      updated: 0,
      skipped: 0,
      failed: 0,
      items: []
    }
    post.mockResolvedValue({ data: response })

    const result = await syncFromCrs(payload)

    expect(post).toHaveBeenCalledWith('/admin/accounts/sync/crs', payload, {
      timeout: 180_000
    })
    expect(result).toEqual(response)
  })

  it('Codex 重置有明细选择时发送 credit_id', async () => {
    const response = {
      code: 'reset',
      credit_id: 'credit-1',
      redeem_request_id: 'redeem-1',
      windows_reset: 1
    }
    post.mockResolvedValue({ data: response })

    const result = await consumeCodexInviteReset(42, ' credit-1 ')

    expect(post).toHaveBeenCalledWith('/admin/accounts/42/codex/invite-reset/consume', {
      credit_id: 'credit-1'
    })
    expect(result).toEqual(response)
  })

  it('Codex 重置没有明细选择时省略 credit_id', async () => {
    const response = {
      code: 'reset',
      redeem_request_id: 'redeem-2',
      windows_reset: 1
    }
    post.mockResolvedValue({ data: response })

    const result = await consumeCodexInviteReset(42)

    expect(post).toHaveBeenCalledWith('/admin/accounts/42/codex/invite-reset/consume', {})
    expect(result).toEqual(response)
  })
})
