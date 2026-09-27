/**
 * 创作台提交快照（提示词 + 参数）本地存储测试。
 * 使用 fake-indexeddb 提供内存版 IndexedDB。
 */
import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'

import { __resetCreativeStoreForTest, clearAll } from '../creativeLocalStore'
import {
  CREATIVE_RUN_INPUT_LIMIT,
  listRecentPrompts,
  loadRunInputSnapshot,
  pruneRunInputIndex,
  saveRunInputSnapshot,
  type CreativeRunInputSnapshot,
} from '../creativeRunInputs'

function snapshot(runId: string, overrides: Partial<CreativeRunInputSnapshot> = {}): CreativeRunInputSnapshot {
  return {
    runId,
    prompt: `prompt-${runId}`,
    operation: 'generate',
    model: 'gemini-3.1-flash-lite-image',
    groupId: '5',
    imageSize: '1K',
    aspectRatio: '9:16',
    quality: '',
    background: '',
    thinkingLevel: '',
    createdAt: Date.now(),
    ...overrides,
  }
}

describe('pruneRunInputIndex', () => {
  it('去空、去重并保留原顺序', () => {
    expect(pruneRunInputIndex(['a', '', 'b', 'a', '  ', 'c'], 10)).toEqual({ kept: ['a', 'b', 'c'], dropped: [] })
  })

  it('超过上限时按最旧优先淘汰', () => {
    expect(pruneRunInputIndex(['a', 'b', 'c'], 2)).toEqual({ kept: ['a', 'b'], dropped: ['c'] })
  })

  it('上限为 0 时全部淘汰', () => {
    expect(pruneRunInputIndex(['a'], 0)).toEqual({ kept: [], dropped: ['a'] })
  })
})

describe('run input snapshot store', () => {
  beforeEach(async () => {
    __resetCreativeStoreForTest()
    await clearAll()
  })

  it('保存后可读回完整快照', async () => {
    await saveRunInputSnapshot(snapshot('crun_1'))
    const loaded = await loadRunInputSnapshot('crun_1')
    expect(loaded?.prompt).toBe('prompt-crun_1')
    expect(loaded?.aspectRatio).toBe('9:16')
    expect(loaded?.groupId).toBe('5')
  })

  it('不存在的 run 返回 null', async () => {
    expect(await loadRunInputSnapshot('crun_missing')).toBeNull()
  })

  it('超过上限时清理最旧快照', async () => {
    const total = CREATIVE_RUN_INPUT_LIMIT + 2
    for (let i = 0; i < total; i += 1) {
      await saveRunInputSnapshot(snapshot(`crun_${i}`))
    }
    expect(await loadRunInputSnapshot(`crun_${total - 1}`)).not.toBeNull()
    expect(await loadRunInputSnapshot('crun_0')).toBeNull()
    expect(await loadRunInputSnapshot('crun_1')).toBeNull()
  })

  it('同一 run 重复保存不会重复占位', async () => {
    await saveRunInputSnapshot(snapshot('crun_dup', { prompt: 'first' }))
    await saveRunInputSnapshot(snapshot('crun_dup', { prompt: 'second' }))
    const loaded = await loadRunInputSnapshot('crun_dup')
    expect(loaded?.prompt).toBe('second')
  })
})

describe('listRecentPrompts', () => {
  beforeEach(async () => {
    __resetCreativeStoreForTest()
    await clearAll()
  })

  it('按最近使用倒序返回，并按提示词文本去重', async () => {
    await saveRunInputSnapshot(snapshot('crun_1', { prompt: '甲' }))
    await saveRunInputSnapshot(snapshot('crun_2', { prompt: '乙' }))
    await saveRunInputSnapshot(snapshot('crun_3', { prompt: '甲' }))

    const prompts = await listRecentPrompts(10)
    // 甲 最后使用（crun_3）在最前，且只出现一次
    expect(prompts.map((item) => item.prompt)).toEqual(['甲', '乙'])
    expect(prompts[0].runId).toBe('crun_3')
  })

  it('遵守数量上限', async () => {
    for (const prompt of ['一', '二', '三']) {
      await saveRunInputSnapshot(snapshot(`crun_${prompt}`, { prompt }))
    }
    expect((await listRecentPrompts(2)).map((item) => item.prompt)).toEqual(['三', '二'])
    expect(await listRecentPrompts(0)).toEqual([])
  })

  it('没有快照时返回空数组', async () => {
    expect(await listRecentPrompts(10)).toEqual([])
  })
})
