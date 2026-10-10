import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))

import { getAvailable } from '@/api/groups'

describe('available groups', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: [{ id: 1, name: 'internal', display_name: 'Display' }] })
  })

  it('默认保留模型目录，筛选查询可以跳过模型解析', async () => {
    const groups = await getAvailable('personal', 7)
    expect(get).toHaveBeenLastCalledWith('/groups/available', { params: { scope: 'personal', subscription_id: 7 } })
    expect(groups[0]).toMatchObject({ name: 'Display', canonical_name: 'internal' })

    await getAvailable('personal', undefined, false)
    expect(get).toHaveBeenLastCalledWith('/groups/available', { params: { scope: 'personal', include_models: false } })
  })
})
