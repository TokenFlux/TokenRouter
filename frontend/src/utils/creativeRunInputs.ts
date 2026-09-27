/**
 * 创作台「本次提交的输入快照」本地存储。
 *
 * 为什么必须存本地：服务端按创作台隐私不变量**只保存 prompt 的 sha256**，不保存明文，
 * 因此失败任务重试时无法从服务端取回提示词；只能在创建任务时把提交参数留在当前浏览器。
 *
 * 存储复用 creativeLocalStore 的 settings store（key → value），避免为单个用途升级 IndexedDB 版本；
 * 另用一条索引键维护顺序并做容量裁剪。
 */

import { deleteSetting, loadSetting, saveSetting } from './creativeLocalStore'

// 单次提交的输入快照：足以还原输入区并重新提交（源图/mask 由画布在点击生成时重新采集）
export interface CreativeRunInputSnapshot {
  runId: string
  prompt: string
  operation: string
  model: string
  groupId: string
  imageSize: string
  aspectRatio: string
  quality: string
  background: string
  thinkingLevel: string
  createdAt: number
}

const SNAPSHOT_KEY_PREFIX = 'creative-run-input:'
const SNAPSHOT_INDEX_KEY = 'creative-run-input-index'

// 最多保留多少条历史提交快照，超出按最旧优先清理
export const CREATIVE_RUN_INPUT_LIMIT = 50

export function runInputSnapshotKey(runId: string): string {
  return SNAPSHOT_KEY_PREFIX + runId
}

// 把新 runId 放到索引最前并去重，再按上限裁剪；返回保留与淘汰的 id。
export function pruneRunInputIndex(
  index: string[],
  limit: number = CREATIVE_RUN_INPUT_LIMIT,
): { kept: string[]; dropped: string[] } {
  const bounded = Math.max(0, limit)
  const seen = new Set<string>()
  const deduped: string[] = []
  for (const id of index) {
    const normalized = String(id ?? '').trim()
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    deduped.push(normalized)
  }
  return { kept: deduped.slice(0, bounded), dropped: deduped.slice(bounded) }
}

// 保存一次提交快照；本地存储不可用时静默失败，不影响提交本身。
export async function saveRunInputSnapshot(snapshot: CreativeRunInputSnapshot): Promise<void> {
  try {
    await saveSetting(runInputSnapshotKey(snapshot.runId), snapshot)
    const current = (await loadSetting<string[]>(SNAPSHOT_INDEX_KEY)) ?? []
    const { kept, dropped } = pruneRunInputIndex([snapshot.runId, ...current])
    await saveSetting(SNAPSHOT_INDEX_KEY, kept)
    await Promise.all(dropped.map((id) => deleteSetting(runInputSnapshotKey(id))))
  } catch {
    // 本地存储不可用（隐私模式 / 配额不足）时放弃快照，仅影响重试可用性
  }
}

// 读取某次提交的快照，不存在返回 null。
export async function loadRunInputSnapshot(runId: string): Promise<CreativeRunInputSnapshot | null> {
  try {
    return await loadSetting<CreativeRunInputSnapshot>(runInputSnapshotKey(runId))
  } catch {
    return null
  }
}

// 列出最近用过的提示词（按时间倒序、按文本去重），供输入区快速切换。
// 同一提示词重复提交只保留最新一条。
export async function listRecentPrompts(limit = 10): Promise<CreativeRunInputSnapshot[]> {
  const bounded = Math.max(0, limit)
  if (bounded === 0) return []
  try {
    const index = (await loadSetting<string[]>(SNAPSHOT_INDEX_KEY)) ?? []
    const out: CreativeRunInputSnapshot[] = []
    const seen = new Set<string>()
    for (const runId of index) {
      if (out.length >= bounded) break
      const snapshot = await loadSetting<CreativeRunInputSnapshot>(runInputSnapshotKey(runId))
      if (!snapshot) continue
      const prompt = String(snapshot.prompt ?? '').trim()
      if (!prompt || seen.has(prompt)) continue
      seen.add(prompt)
      out.push(snapshot)
    }
    return out
  } catch {
    return []
  }
}
