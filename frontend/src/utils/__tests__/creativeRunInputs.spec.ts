/**
 * 创作台提交快照（提示词 + 参数）本地存储测试。
 * 使用 fake-indexeddb 提供内存版 IndexedDB。
 */
import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'

import { __resetCreativeStoreForTest, clearAll } from '../creativeLocalStore'
import {
  loadRunInputSnapshot,
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

  it('同一 run 重复保存覆盖旧值', async () => {
    await saveRunInputSnapshot(snapshot('crun_dup', { prompt: 'first' }))
    await saveRunInputSnapshot(snapshot('crun_dup', { prompt: 'second' }))
    const loaded = await loadRunInputSnapshot('crun_dup')
    expect(loaded?.prompt).toBe('second')
  })
})
