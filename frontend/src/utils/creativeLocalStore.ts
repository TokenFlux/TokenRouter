/**
 * creativeLocalStore.ts stores creative images, scene snapshots, settings, and
 * submitted parameters in IndexedDB. Asset keys connect canvas image metadata
 * to Blob records, while run parameter keys share the settings store so local
 * clearing removes prompts with the other creative data. The workspace UUID
 * lives separately in localStorage and identifies this browser to history
 * requests.
 *
 * Database opening is shared through one cached promise, with request errors
 * normalized for quota and unavailable-storage messages. Run parameter writes
 * precede retention pruning in the same object store transaction. Public
 * operations await request success rather than transaction completion;
 * multi-store clearing uses separate transactions. Callers coordinate scene
 * snapshots, output acknowledgements, and workspace rotation around these local
 * operations.
 */

/**
 * DB_NAME and DB_VERSION identify the origin-local creative database opened and
 * upgraded by this module.
 */
const DB_NAME = 'tokenrouter-creative-studio'
const DB_VERSION = 1

/**
 * STORE_ASSETS, STORE_SCENES, and STORE_SETTINGS separate image bytes, canvas
 * JSON, and creative preferences by record purpose.
 */
const STORE_ASSETS = 'assets'
const STORE_SCENES = 'scenes'
const STORE_SETTINGS = 'settings'

/**
 * CREATIVE_WORKSPACE_STORAGE_KEY names the origin-shared browser identity used
 * for server history requests across tabs.
 */
export const CREATIVE_WORKSPACE_STORAGE_KEY = 'creative:workspaceId'
/**
 * CREATIVE_WORKSPACE_UUID_RE validates stored identifiers before they can be
 * reused in creative workspace requests.
 */
const CREATIVE_WORKSPACE_UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

/**
 * LocalAssetKind distinguishes uploaded sources, masks, and recovered generated
 * image outputs.
 */
export type LocalAssetKind = 'source' | 'mask' | 'output'

/**
 * LocalAsset stores image bytes and canvas metadata under one local key. Output
 * records pair runId and outputIndex for history lookup; source and mask
 * records may omit both server identity fields.
 */
export interface LocalAsset {
  key: string // local record identifier
  kind: LocalAssetKind // stored image category
  blob: Blob // stored image bytes
  runId?: string // server run identifier
  outputIndex?: number // zero-based output index
  createdAt: number // creation time milliseconds
}

/**
 * LocalSceneRecord stores a named Fabric scene snapshot as serialized JSON
 * text.
 */
export interface LocalSceneRecord {
  key: string // local record identifier
  json: string // serialized Fabric scene
  updatedAt: number // update time milliseconds
}

/**
 * LocalSettingRecord wraps arbitrary creative settings with their key and
 * update time.
 */
export interface LocalSettingRecord {
  key: string // local record identifier
  value: unknown // structured-cloneable stored value
  updatedAt: number // update time milliseconds
}

/**
 * LocalRunParams retains submitted prompt and parameter values for browser
 * history reuse. quality, background, and thinkingLevel can be empty when
 * unsupported; history omits those empty tags while applyRunParams normalizes
 * restored controls.
 */
export interface LocalRunParams {
  outputFormat?: string // Submitted output format name.
  frameCount?: number // Submitted animation frame count.
  frameDurationMS?: number // Submitted frame delay, milliseconds.
  runId: string // server run identifier
  optionKey: string // group_id::model selector key
  groupName: string // submitted group label
  operation: string // submitted image operation
  prompt: string // submitted prompt text
  imageSize: string // submitted size tier
  aspectRatio: string // submitted aspect ratio
  quality: string // submitted quality tier
  background: string // submitted background option
  thinkingLevel: string // submitted reasoning level
  referenceCount: number // uploaded references excluding mask
  createdAt: number // creation time milliseconds
}

/**
 * LocalStoreErrorType selects quota, unavailable-storage, or unknown UI
 * recovery handling.
 */
export type LocalStoreErrorType = 'quota' | 'unavailable' | 'unknown'

/**
 * LocalStoreError carries a browser storage failure category and optional
 * underlying cause.
 */
export class LocalStoreError extends Error {
  readonly type: LocalStoreErrorType // storage failure category

  /**
   * constructor records the classified local-storage failure and optional
   * original cause. Consumers use the type field for recovery messages, while the
   * attached cause retains the underlying browser error for diagnosis.
   */
  constructor(type: LocalStoreErrorType, message: string, cause?: unknown) {
    super(message)
    this.name = 'LocalStoreError'
    this.type = type
    if (cause !== undefined) {
      const target = this as {
        cause?: unknown // underlying storage failure cause
      }
      target.cause = cause
    }
  }
}

/**
 * LocalStoreQuotaError identifies exhausted browser storage for the creative
 * backup warning.
 */
export class LocalStoreQuotaError extends LocalStoreError {
  /**
   * constructor creates a quota-classified storage error for callers that need a
   * download-and-backup message. The default message describes exhausted local
   * storage, and the optional cause records the originating browser failure.
   */
  constructor(message = 'Local storage quota exceeded', cause?: unknown) {
    super('quota', message, cause)
    this.name = 'LocalStoreQuotaError'
  }
}

/**
 * outputAssetKey joins the server run and output index into a stable asset key.
 */
export function outputAssetKey(runId: string, outputIndex: number): string {
  return `output:${runId}:${outputIndex}`
}

/**
 * localAssetKey prefixes a local identifier with its supplied asset kind.
 */
export function localAssetKey(kind: LocalAssetKind, localId: string): string {
  return `${kind}:local:${localId}`
}

/**
 * normalizeCreativeWorkspaceId validates a persisted browser workspace UUID and
 * normalizes its case. Nonstring input or a value outside the supported UUID
 * pattern returns null, prompting identity creation by the caller.
 */
export function normalizeCreativeWorkspaceId(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const normalized = value.trim().toLowerCase()
  return CREATIVE_WORKSPACE_UUID_RE.test(normalized) ? normalized : null
}

/**
 * generateCreativeWorkspaceId creates a lowercase browser workspace UUID from
 * available cryptographic randomness. It prefers randomUUID, otherwise sets
 * UUID version and variant bits on random bytes. Environments without either
 * API raise an unavailable-storage error.
 */
function generateCreativeWorkspaceId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID().toLowerCase()
  }
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    /**
     * When randomUUID is unavailable, cryptographic random bytes receive
     * version-four and RFC variant bits before UUID string formatting.
     */
    const bytes = new Uint8Array(16)
    crypto.getRandomValues(bytes)
    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  throw new LocalStoreError('unavailable', 'Creative workspace identity is unavailable')
}

/**
 * getCreativeWorkspaceId returns this browser origin's persisted creative
 * workspace identifier. An absent or invalid stored value is replaced with a
 * generated UUID before returning. Storage and randomness failures raise
 * LocalStoreError so callers stop workspace requests rather than using a shared
 * identity.
 */
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

/**
 * rotateCreativeWorkspaceId stores and returns a newly generated browser
 * workspace identifier. Callers use it after clearing local creative records;
 * unavailable storage or randomness raises LocalStoreError and leaves success
 * reporting to the caller.
 */
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

/**
 * idbRequest adapts one IndexedDB request to a promise with classified storage
 * errors. Resolution follows the request success event; callers needing
 * transaction completion must observe that separately from this helper.
 */
function idbRequest<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(toLocalStoreError(request.error))
  })
}

/**
 * toLocalStoreError preserves classified storage failures and maps browser
 * errors to UI categories. QuotaExceededError and code 22 become quota errors;
 * recognized unavailable cases retain their cause, and remaining failures use
 * the unknown category.
 */
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

/**
 * dbPromise shares the open connection across operations and resets after
 * request errors or blocked opening.
 */
let dbPromise: Promise<IDBDatabase> | null = null

/**
 * openStore opens the creative database and shares the pending result across
 * callers. An upgrade creates missing asset, scene, and settings stores, with a
 * kind index for assets. Request errors and blocked opens reset the cached
 * promise so a later call can retry. The missing-indexedDB rejection remains
 * cached until the test reset helper clears it, and successful connections stay
 * cached for this module lifetime.
 */
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

/**
 * withStore opens one transaction and runs the caller's request against its
 * selected object store. The promise follows request success and normalizes
 * synchronous transaction failures; it does not wait for the transaction
 * completion event.
 */
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

/**
 * saveAsset replaces the record at an asset key and returns the supplied
 * object. Repeating a key overwrites its Blob and metadata; request failures
 * propagate as classified local-storage errors.
 */
export async function saveAsset(asset: LocalAsset): Promise<LocalAsset> {
  await withStore(STORE_ASSETS, 'readwrite', (store) => store.put(asset))
  return asset
}

/**
 * loadAsset reads one stored image and its metadata by exact key. Missing keys
 * produce null, while browser storage failures reject with LocalStoreError for
 * the caller's recovery path.
 */
export async function loadAsset(key: string): Promise<LocalAsset | null> {
  const record = await withStore(
    STORE_ASSETS,
    'readonly',
    (store) => store.get(key) as IDBRequest<LocalAsset | undefined>,
  )
  return record ?? null
}

/**
 * listAssets returns assets of one kind ordered by ascending creation time. It
 * uses the kind index when available and otherwise filters all records,
 * allowing lookup against stores missing that index.
 */
export async function listAssets(kind: LocalAssetKind): Promise<LocalAsset[]> {
  const db = await openStore()
  try {
    const store = db.transaction(STORE_ASSETS, 'readonly').objectStore(STORE_ASSETS)
    let records: LocalAsset[]
    /**
     * Use the kind index when available; older stores without that index still
     * supply the requested assets through filtering.
     */
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

/**
 * deleteAsset removes a single stored asset by its exact key. Missing keys are
 * successful no-ops, and callers handle any classified storage failure before
 * treating removal as successful.
 */
export async function deleteAsset(key: string): Promise<void> {
  await withStore(STORE_ASSETS, 'readwrite', (store) => store.delete(key))
}

/**
 * clearKind lists one asset kind and deletes those records through a readwrite
 * transaction. Enumeration precedes deletion, so records created after the
 * initial list are outside this operation's selected set.
 */
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

/**
 * saveSceneJson replaces a named scene snapshot with the supplied JSON text. It
 * adds a browser millisecond timestamp and delegates storage errors to the
 * caller; scene serialization remains the canvas owner's responsibility.
 */
export async function saveSceneJson(key: string, json: string): Promise<void> {
  const record: LocalSceneRecord = { key, json, updatedAt: Date.now() }
  await withStore(STORE_SCENES, 'readwrite', (store) => store.put(record))
}

/**
 * loadSceneJson returns the stored JSON string for a named scene. Missing
 * snapshots return null, and the canvas decides how to recover malformed
 * content after this read succeeds.
 */
export async function loadSceneJson(key: string): Promise<string | null> {
  const record = await withStore(
    STORE_SCENES,
    'readonly',
    (store) => store.get(key) as IDBRequest<LocalSceneRecord | undefined>,
  )
  return record?.json ?? null
}

/**
 * saveSetting replaces a named setting with its supplied structured-cloneable
 * value. The record includes a browser millisecond update timestamp; callers
 * serialize competing writes when latest-edit ordering matters.
 */
export async function saveSetting(key: string, value: unknown): Promise<void> {
  const record: LocalSettingRecord = { key, value, updatedAt: Date.now() }
  await withStore(STORE_SETTINGS, 'readwrite', (store) => store.put(record))
}

/**
 * loadSetting returns a stored setting value cast to the caller's requested
 * type. Missing records return null; the generic annotation supplies no runtime
 * validation of stored browser data.
 */
export async function loadSetting<T = unknown>(key: string): Promise<T | null> {
  const record = await withStore(
    STORE_SETTINGS,
    'readonly',
    (store) => store.get(key) as IDBRequest<LocalSettingRecord | undefined>,
  )
  return record ? (record.value as T) : null
}

/**
 * RUN_PARAMS_KEY_PREFIX separates submitted task parameters from ordinary
 * preferences inside the shared settings object store.
 */
const RUN_PARAMS_KEY_PREFIX = 'run:'
/**
 * MAX_RUN_PARAMS_RECORDS bounds retained prompt snapshots while allowing more
 * records than one visible history page.
 */
const MAX_RUN_PARAMS_RECORDS = 200

/**
 * runParamsKeyRange selects settings keys belonging to persisted creative run
 * parameters.
 */
function runParamsKeyRange(): IDBKeyRange {
  return IDBKeyRange.bound(RUN_PARAMS_KEY_PREFIX, `${RUN_PARAMS_KEY_PREFIX}\uffff`)
}

/**
 * saveRunParams saves submitted parameters and prunes the oldest stored
 * parameter records. The run-prefixed key keeps these records distinct from
 * form settings, and retention sorts by the enclosing record's update time.
 * Put, enumerate, and delete requests share one transaction so pruning sees the
 * just-written record. Request success drives the returned promise; callers
 * retain their in-memory parameter snapshot when storage reports a failure.
 */
export async function saveRunParams(params: LocalRunParams): Promise<void> {
  const record: LocalSettingRecord = {
    key: `${RUN_PARAMS_KEY_PREFIX}${params.runId}`,
    value: params,
    updatedAt: Date.now(),
  }
  const db = await openStore()
  try {
    const store = db.transaction(STORE_SETTINGS, 'readwrite').objectStore(STORE_SETTINGS)
    /**
     * Write the parameter record before enumerating retention candidates in the
     * same transaction so the new snapshot participates in pruning.
     */
    await idbRequest(store.put(record))
    const records = await idbRequest(
      store.getAll(runParamsKeyRange()) as IDBRequest<LocalSettingRecord[]>,
    )
    if (records.length <= MAX_RUN_PARAMS_RECORDS) return
    records.sort((a, b) => a.updatedAt - b.updatedAt)
    for (const stale of records.slice(0, records.length - MAX_RUN_PARAMS_RECORDS)) {
      await idbRequest(store.delete(stale.key))
    }
  } catch (error) {
    throw toLocalStoreError(error)
  }
}

/**
 * loadRunParamsMap indexes stored parameter snapshots by each value's run
 * identifier. Values lacking a string runId are skipped, while other fields
 * remain unchecked and are interpreted by history consumers.
 */
export async function loadRunParamsMap(): Promise<Map<string, LocalRunParams>> {
  const records = await withStore(
    STORE_SETTINGS,
    'readonly',
    (store) => store.getAll(runParamsKeyRange()) as IDBRequest<LocalSettingRecord[]>,
  )
  const map = new Map<string, LocalRunParams>()
  for (const record of records) {
    const params = record.value as LocalRunParams | null
    /**
     * Skip stored values without a usable run identifier so unrelated malformed
     * records do not appear as task parameters in the returned index.
     */
    if (params && typeof params.runId === 'string') map.set(params.runId, params)
  }
  return map
}

/**
 * clearAll clears creative assets, scenes, and settings in that order. Each
 * store uses its own transaction, so a later failure can leave earlier stores
 * cleared; workspace rotation is the caller's subsequent step.
 */
export async function clearAll(): Promise<void> {
  const db = await openStore()
  try {
    /**
     * Clear stores sequentially, propagating the first failure to the caller before
     * it rotates the browser workspace identity.
     */
    for (const storeName of [STORE_ASSETS, STORE_SCENES, STORE_SETTINGS]) {
      await idbRequest(db.transaction(storeName, 'readwrite').objectStore(storeName).clear())
    }
  } catch (error) {
    throw toLocalStoreError(error)
  }
}

/**
 * __resetCreativeStoreForTest drops the cached database-open promise between
 * isolated test environments. It does not close an existing connection or
 * delete records, so tests own database cleanup separately.
 */
export function __resetCreativeStoreForTest(): void {
  dbPromise = null
}
