/**
 * 创作台本地存储（IndexedDB）
 * 设计要点：
 * 图片素材（原图、mask、输出）保存在当前浏览器的 IndexedDB。
 * - 所有 API Promise 化且幂等（重复 put/delete 同一 key 结果一致）。
 * - 配额不足时抛 LocalStoreQuotaError，由 UI 提示用户下载备份，素材留在当前浏览器。
 */

// ==================== 常量与类型 ====================

const DB_NAME = 'tokenrouter-creative-studio'
const DB_VERSION = 1

const STORE_ASSETS = 'assets'
const STORE_SCENES = 'scenes'
const STORE_SETTINGS = 'settings'

// 创作台浏览器工作区标识：同源标签页共享，不同浏览器拥有独立值。
export const CREATIVE_WORKSPACE_STORAGE_KEY = 'creative:workspaceId'
const CREATIVE_WORKSPACE_UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

// 素材种类：源图 / mask / AI 输出
export type LocalAssetKind = 'source' | 'mask' | 'output'

// 本地素材记录，blob 为图片二进制本体
export interface LocalAsset {
  key: string
  kind: LocalAssetKind
  blob: Blob
  runId?: string
  outputIndex?: number
  createdAt: number
}

// 场景 JSON 快照（fabric canvas toJSON 结果）
export interface LocalSceneRecord {
  key: string
  json: string
  updatedAt: number
}

// 设置项（上次选择的模型、尺寸等恢复用）
export interface LocalSettingRecord {
  key: string
  value: unknown
  updatedAt: number
}

// 本地存储错误类型
export type LocalStoreErrorType = 'quota' | 'unavailable' | 'unknown'

export class LocalStoreError extends Error {
  readonly type: LocalStoreErrorType

  constructor(type: LocalStoreErrorType, message: string, cause?: unknown) {
    super(message)
    this.name = 'LocalStoreError'
    this.type = type
    // 保留原始错误便于排查
    if (cause !== undefined) {
      const target = this as { cause?: unknown }
      target.cause = cause
    }
  }
}

// 配额不足专用错误，UI 据此提示“空间不足请下载备份”
export class LocalStoreQuotaError extends LocalStoreError {
  constructor(message = 'Local storage quota exceeded', cause?: unknown) {
    super('quota', message, cause)
    this.name = 'LocalStoreQuotaError'
  }
}

// ==================== key 组合规则 ====================

// 输出素材 key：runId + 输出序号（关联服务端 run 元信息）
export function outputAssetKey(runId: string, outputIndex: number): string {
  return `output:${runId}:${outputIndex}`
}

// 本地素材 key（源图 / mask 等用户本地产物）
export function localAssetKey(kind: LocalAssetKind, localId: string): string {
  return `${kind}:local:${localId}`
}

// 校验并规范化浏览器工作区 UUID，发往服务端的标识需要符合 UUID 格式。
export function normalizeCreativeWorkspaceId(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const normalized = value.trim().toLowerCase()
  return CREATIVE_WORKSPACE_UUID_RE.test(normalized) ? normalized : null
}

function generateCreativeWorkspaceId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID().toLowerCase()
  }
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = new Uint8Array(16)
    crypto.getRandomValues(bytes)
    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  throw new LocalStoreError('unavailable', 'Creative workspace identity is unavailable')
}

// 读取或创建当前浏览器工作区；失败时 fail-close，不回退到共享工作区。
export function getCreativeWorkspaceId(): string {
  if (typeof window === 'undefined') {
    throw new LocalStoreError('unavailable', 'Browser local storage is unavailable')
  }
  try {
    const storage = window.localStorage
    const current = normalizeCreativeWorkspaceId(storage.getItem(CREATIVE_WORKSPACE_STORAGE_KEY))
    if (current) return current
    const generated = generateCreativeWorkspaceId()
    storage.setItem(CREATIVE_WORKSPACE_STORAGE_KEY, generated)
    return generated
  } catch (error) {
    if (error instanceof LocalStoreError) throw error
    throw new LocalStoreError('unavailable', 'Browser local storage is unavailable', error)
  }
}

// 旋转当前浏览器工作区，使清空本机数据后旧历史不可见。
export function rotateCreativeWorkspaceId(): string {
  if (typeof window === 'undefined') {
    throw new LocalStoreError('unavailable', 'Browser local storage is unavailable')
  }
  try {
    const generated = generateCreativeWorkspaceId()
    window.localStorage.setItem(CREATIVE_WORKSPACE_STORAGE_KEY, generated)
    return generated
  } catch (error) {
    if (error instanceof LocalStoreError) throw error
    throw new LocalStoreError('unavailable', 'Browser local storage is unavailable', error)
  }
}

// ==================== 内部工具 ====================

// IDBRequest → Promise 化
function idbRequest<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(toLocalStoreError(request.error))
  })
}

// 统一错误归一化：QuotaExceededError 单独识别
function toLocalStoreError(error: unknown): LocalStoreError {
  if (error instanceof LocalStoreError) return error
  const name = (error as DOMException | null)?.name || ''
  if (name === 'QuotaExceededError' || (error as DOMException)?.code === 22) {
    return new LocalStoreQuotaError('Local storage quota exceeded', error)
  }
  if (
    name === 'InvalidStateError' ||
    name === 'SecurityError' ||
    name === 'NotSupportedError' ||
    name === 'UnknownError'
  ) {
    return new LocalStoreError('unavailable', 'Local storage unavailable', error)
  }
  return new LocalStoreError('unknown', 'Local storage operation failed', error)
}

// 打开（并升级）数据库，进程内缓存打开 Promise
let dbPromise: Promise<IDBDatabase> | null = null

function openStore(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise
  dbPromise = new Promise((resolve, reject) => {
    if (typeof indexedDB === 'undefined') {
      reject(new LocalStoreError('unavailable', 'IndexedDB is not supported in this environment'))
      return
    }
    const request = indexedDB.open(DB_NAME, DB_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      // 三个 store 均以 key 为主键；assets 额外建 kind 索引用于按类列举
      if (!db.objectStoreNames.contains(STORE_ASSETS)) {
        const store = db.createObjectStore(STORE_ASSETS, { keyPath: 'key' })
        store.createIndex('kind', 'kind', { unique: false })
      }
      if (!db.objectStoreNames.contains(STORE_SCENES)) {
        db.createObjectStore(STORE_SCENES, { keyPath: 'key' })
      }
      if (!db.objectStoreNames.contains(STORE_SETTINGS)) {
        db.createObjectStore(STORE_SETTINGS, { keyPath: 'key' })
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => {
      dbPromise = null
      reject(toLocalStoreError(request.error))
    }
    request.onblocked = () => {
      dbPromise = null
      reject(new LocalStoreError('unavailable', 'Local storage is blocked by another session'))
    }
  })
  return dbPromise
}

// 便捷封装：在指定 store 上执行一个读写操作
async function withStore<T>(
  storeName: string,
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const db = await openStore()
  try {
    return await idbRequest(run(db.transaction(storeName, mode).objectStore(storeName)))
  } catch (error) {
    throw toLocalStoreError(error)
  }
}

// ==================== assets API ====================

// 保存素材（同 key 覆盖写，天然幂等）
export async function saveAsset(asset: LocalAsset): Promise<LocalAsset> {
  await withStore(STORE_ASSETS, 'readwrite', (store) => store.put(asset))
  return asset
}

// 读取单个素材，不存在时返回 null
export async function loadAsset(key: string): Promise<LocalAsset | null> {
  const record = await withStore(STORE_ASSETS, 'readonly', (store) =>
    store.get(key) as IDBRequest<LocalAsset | undefined>,
  )
  return record ?? null
}

// 按种类列举素材（按创建时间升序）
export async function listAssets(kind: LocalAssetKind): Promise<LocalAsset[]> {
  const db = await openStore()
  try {
    const store = db.transaction(STORE_ASSETS, 'readonly').objectStore(STORE_ASSETS)
    // 优先走 kind 索引；索引不可用时退化为全量过滤
    let records: LocalAsset[]
    if (store.indexNames.contains('kind')) {
      records = await idbRequest(store.index('kind').getAll(kind) as IDBRequest<LocalAsset[]>)
    } else {
      records = await idbRequest(store.getAll() as IDBRequest<LocalAsset[]>)
      records = records.filter((item) => item.kind === kind)
    }
    return records.sort((a, b) => a.createdAt - b.createdAt)
  } catch (error) {
    throw toLocalStoreError(error)
  }
}

// 删除单个素材，key 不存在时视为成功（幂等）
export async function deleteAsset(key: string): Promise<void> {
  await withStore(STORE_ASSETS, 'readwrite', (store) => store.delete(key))
}

// 清空某个种类的全部素材
export async function clearKind(kind: LocalAssetKind): Promise<void> {
  const assets = await listAssets(kind)
  const db = await openStore()
  try {
    const store = db.transaction(STORE_ASSETS, 'readwrite').objectStore(STORE_ASSETS)
    for (const asset of assets) {
      await idbRequest(store.delete(asset.key))
    }
  } catch (error) {
    throw toLocalStoreError(error)
  }
}

// ==================== scenes API ====================

// 保存画布场景 JSON 快照
export async function saveSceneJson(key: string, json: string): Promise<void> {
  const record: LocalSceneRecord = { key, json, updatedAt: Date.now() }
  await withStore(STORE_SCENES, 'readwrite', (store) => store.put(record))
}

// 读取场景 JSON，不存在时返回 null
export async function loadSceneJson(key: string): Promise<string | null> {
  const record = await withStore(STORE_SCENES, 'readonly', (store) =>
    store.get(key) as IDBRequest<LocalSceneRecord | undefined>,
  )
  return record?.json ?? null
}

// ==================== settings API ====================

// 保存设置项（上次选择恢复用）
export async function saveSetting(key: string, value: unknown): Promise<void> {
  const record: LocalSettingRecord = { key, value, updatedAt: Date.now() }
  await withStore(STORE_SETTINGS, 'readwrite', (store) => store.put(record))
}

// 读取设置项，不存在时返回 null
export async function loadSetting<T = unknown>(key: string): Promise<T | null> {
  const record = await withStore(STORE_SETTINGS, 'readonly', (store) =>
    store.get(key) as IDBRequest<LocalSettingRecord | undefined>,
  )
  return record ? (record.value as T) : null
}

// 删除设置项，key 不存在时视为成功（幂等）
export async function deleteSetting(key: string): Promise<void> {
  await withStore(STORE_SETTINGS, 'readwrite', (store) => store.delete(key))
}

// ==================== 维护 API ====================

// 清空创作台全部本地数据（素材 + 场景 + 设置）
export async function clearAll(): Promise<void> {
  const db = await openStore()
  try {
    for (const storeName of [STORE_ASSETS, STORE_SCENES, STORE_SETTINGS]) {
      await idbRequest(db.transaction(storeName, 'readwrite').objectStore(storeName).clear())
    }
  } catch (error) {
    throw toLocalStoreError(error)
  }
}

// 测试专用：重置数据库打开缓存，让各用例独立初始化。
export function __resetCreativeStoreForTest(): void {
  dbPromise = null
}
