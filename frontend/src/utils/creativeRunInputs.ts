/**
 * 创作台「本次提交的输入快照」本地存储。
 *
 * 为什么必须存本地：服务端按创作台隐私不变量**只保存 prompt 的 sha256**，不保存明文，
 * 因此历史里的复制提示词和失败重试都取不到服务端明文；只能在创建任务时把提交参数留在当前浏览器。
 *
 * 存储复用 creativeLocalStore 的 settings store（key → value），避免为单个用途升级 IndexedDB 版本；
 * 快照按 runId 一条，与本地素材同生命周期（清空本机创作数据时一并清除）。
 */

import { loadSetting, saveSetting } from './creativeLocalStore'

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

export function runInputSnapshotKey(runId: string): string {
  return SNAPSHOT_KEY_PREFIX + runId
}

// 保存一次提交快照；本地存储不可用时静默失败，不影响提交本身。
export async function saveRunInputSnapshot(snapshot: CreativeRunInputSnapshot): Promise<void> {
  try {
    await saveSetting(runInputSnapshotKey(snapshot.runId), snapshot)
  } catch {
    // 本地存储不可用（隐私模式 / 配额不足）时放弃快照，仅影响历史复制与重试
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
