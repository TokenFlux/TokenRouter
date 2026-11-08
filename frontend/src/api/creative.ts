import { apiClient } from './client'

/**
 * creative.ts sends session-authenticated Creative Studio requests and describes
 * model capabilities, run metadata and transient outputs. Every run operation
 * carries the browser workspace ID, while creation accepts an assembled
 * multipart form and an optional idempotency key. The shared client supplies the
 * session token used for these personal image jobs.
 *
 * History and active-run responses are normalized for their callers, retaining
 * separate total-based and cursor-based pagination shapes. Output downloads
 * request Blob data with a longer timeout; callers own local persistence before
 * acknowledgment, which permits transient storage cleanup. Terminal status
 * constants tell polling consumers when execution has ended, independently of
 * whether output bytes have been downloaded and acknowledged.
 */

/**
 * CreativeOperation retains advertised generate, edit and inpaint operations
 * while accepting additional server operation names.
 */
export type CreativeOperation = 'generate' | 'edit' | 'inpaint' | (string & {})

/**
 * CreativeNumericRange defines a server-advertised numeric capability's minimum,
 * maximum and allowed increment.
 */
export interface CreativeNumericRange {
  min: number // Minimum permitted value.
  max: number // Maximum permitted value.
  step: number // Permitted value increment.
}

/**
 * CreativeModelOption combines a group-model identity with the server-advertised
 * generation choices. operations, image_sizes, aspect_ratios, qualities and
 * output_formats govern separate controls. Operation-specific output formats
 * override the legacy combined list; optional compression, background and thinking
 * fields extend those choices when available.
 */
export interface CreativeModelOption {
  group_id: string // Group record identifier.
  group_name: string // Recorded group name.
  model: string // Model identifier value.
  operations: CreativeOperation[] // Supported image operations.
  image_sizes: string[] // Supported image sizes.
  aspect_ratios: string[] // Supported aspect ratios.
  qualities: string[] // Supported quality options.
  gif_frame_count?: CreativeNumericRange // Supported GIF frame counts.
  gif_frame_duration_ms?: CreativeNumericRange // Supported frame delays, milliseconds.
  output_formats: string[] // Supported output formats.
  /**
   * Each admitted operation has its own format choices. An absent map permits
   * older servers to retain their combined output_formats behavior.
   */
  output_formats_by_operation?: Record<string, string[]> // Formats per admitted operation.
  /**
   * Compression bounds are optional capability metadata; callers offer
   * compression controls only when the model advertises usable bounds.
   */
  output_compression?: CreativeNumericRange | null // Advertised compression bounds.
  background_options?: string[] // Supported background options.
  thinking_levels?: string[] // Supported thinking levels.
  max_output_count?: number // Max output count.
  /**
   * The optional model-specific input count limits reference selection when
   * supplied; callers also enforce upload-size capability limits.
   */
  max_reference_images?: number // Max reference images.

  /**
   * The 512 display price applies when the model advertises that image size; it
   * is not a capability declaration by itself.
   */
  price_512?: number // 512 size-tier price.
  price_1k: number // 1K display price.
  price_2k: number // 2K display price.
  price_4k: number // 4K display price.
}

/**
 * CREATIVE_RUN_TERMINAL_STATUSES identifies run outcomes at which the Creative
 * Studio polling consumers stop following execution progress.
 */
export const CREATIVE_RUN_TERMINAL_STATUSES: readonly string[] = [
  'succeeded',
  'failed',
  'cancelled',
  'result_lost',
]

/**
 * CreativeRunStatus represents queue, execution, settlement and terminal run
 * states. Unrecognized strings remain representable so callers can display
 * server states beyond the known polling outcomes.
 */
export type CreativeRunStatus =
  | 'queued'
  | 'running'
  | 'provider_succeeded'
  | 'settlement_pending'
  | 'release_pending'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'result_lost'
  | (string & {})

/**
 * CreativeOutputStatus represents output success, failure, cancellation or
 * acknowledgment while accepting future server status strings.
 */
export type CreativeOutputStatus = 'succeeded' | 'failed' | 'cancelled' | 'acked' | (string & {})

/**
 * CreativeRunOutput describes one run output's status, transient-byte metadata
 * and optional persistence acknowledgment.
 */
export interface CreativeRunOutput {
  frame_count?: number // Actual returned animation frames.
  output_index: number // Zero-based output index.
  status: CreativeOutputStatus // Current server-reported status.
  mime_type?: string // Content MIME type.
  byte_size?: number // Content length bytes.
  /**
   * The server reports an upper expiry time for transient bytes; acknowledgment
   * or server cleanup may remove them before this time.
   */
  transient_expires_at?: number | null // Transient expiry, Unix seconds.
  /**
   * An acknowledgment time means the server accepted local-persistence
   * confirmation; consumers should use their saved local asset for this output.
   */
  acked_at?: number | null // Acknowledgment time, Unix seconds.
  error_code?: string // Recorded error code.
  error_message?: string // Recorded error message.
}

/**
 * CreativeRun describes server execution metadata and optional output slots for
 * one workspace job. Task status and output acknowledgment have separate
 * lifecycles, so a terminal run can still have downloadable unacknowledged
 * output.
 */
export interface CreativeRun {
  frame_count?: number // Requested animation frame count.
  frame_duration_ms?: number // Requested frame delay, milliseconds.
  id: string // Record identifier value.
  status: CreativeRunStatus // Current server-reported status.
  operation: CreativeOperation // Selected image operation.
  model: string // Model identifier value.

  /**
   * The submitted identifier can differ from model after server validation and
   * mapping; callers use it when displaying the requested model.
   */
  requested_model?: string // Recorded requested model.
  group_id: string // Group record identifier.
  requested_output_count: number // Requested output count.
  image_size?: string // Recorded image size.
  aspect_ratio?: string // Recorded aspect ratio.
  output_format?: string // Requested output format.
  estimated_cost?: number // Estimated task cost.
  hold_amount?: number // Reserved task credit.
  actual_cost?: number | null // Actual deducted cost.
  error_code?: string // Recorded error code.
  error_message?: string // Recorded error message.
  created_at?: number // Creation time, Unix seconds.
  started_at?: number | null // Start time, Unix seconds.
  completed_at?: number | null // Completion time, Unix seconds.
  cancelled_at?: number | null // Cancellation time, Unix seconds.
  outputs?: CreativeRunOutput[] // Run output metadata.
}

/**
 * CreativeRunsPage contains normalized history entries and their available total
 * for the Creative Studio history caller.
 */
export interface CreativeRunsPage {
  items: CreativeRun[] // Returned collection entries.
  total: number // Total matching records.
}

/**
 * CreativeActiveRunsPage contains one active-run page and its opaque
 * continuation state. items holds this page, next_cursor selects the next page
 * and has_more tells polling callers whether to continue.
 */
export interface CreativeActiveRunsPage {
  items: CreativeRun[] // Returned collection entries.
  next_cursor: string // Opaque continuation cursor.
  has_more: boolean // More-results availability flag.
}

/**
 * CreativeCapabilities carries prompt and upload limits used by Creative Studio
 * input validation. Asset, combined-input and mask limits are byte counts;
 * prompt length is counted in characters.
 */
export interface CreativeCapabilities {
  max_prompt_chars: number // Maximum prompt characters.
  max_asset_bytes: number // Maximum asset bytes.
  max_total_input_bytes: number // Maximum combined input bytes.
  max_mask_bytes: number // Maximum mask bytes.
  allowed_mime_types: string[] // Accepted image MIME types.
}

/**
 * getCreativeModels retrieves selectable group-model capabilities for Creative
 * Studio. A non-array response becomes an empty list so model selectors receive
 * a collection even when the payload omits usable entries.
 */
export async function getCreativeModels(): Promise<CreativeModelOption[]> {
  const { data } = await apiClient.get<CreativeModelOption[]>('/creative/models')
  /**
   * Treat a missing or malformed model collection as empty so Creative Studio
   * can render its no-model state without iterating invalid data.
   */
  return Array.isArray(data) ? data : []
}

/**
 * getCreativeCapabilities retrieves upload and prompt limits for local
 * validation. Numeric fields are coerced and use built-in limits when falsy;
 * allowed MIME types use the default image list when the response field is not
 * an array.
 */
export async function getCreativeCapabilities(): Promise<CreativeCapabilities> {
  const { data } = await apiClient.get<CreativeCapabilities>('/creative/capabilities')
  /**
   * Use built-in upload and prompt limits when response values are falsy, while
   * retaining a supplied MIME array for input validation.
   */
  return {
    max_prompt_chars: Number(data?.max_prompt_chars) || 8000,
    max_asset_bytes: Number(data?.max_asset_bytes) || 33554432,
    max_total_input_bytes: Number(data?.max_total_input_bytes) || 67108864,
    max_mask_bytes: Number(data?.max_mask_bytes) || 4194304,
    allowed_mime_types: Array.isArray(data?.allowed_mime_types)
      ? data.allowed_mime_types
      : ['image/png', 'image/jpeg', 'image/webp'],
  }
}

/**
 * createCreativeRun submits the caller's assembled multipart form in the
 * selected browser workspace. Reuse the idempotency key when retrying the same
 * form; a truthy key is forwarded with the workspace header.
 */
export async function createCreativeRun(
  form: FormData,
  workspaceId: string,
  idempotencyKey?: string,
): Promise<CreativeRun> {
  const { data } = await apiClient.post<CreativeRun>('/creative/runs', form, {
    headers: {
      /**
       * Override the shared JSON content type because Axios otherwise serializes
       * FormData as JSON, losing the uploaded file representation.
       */
      'Content-Type': 'multipart/form-data',
      'X-Creative-Workspace-ID': workspaceId,
      ...(idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {}),
    },
  })
  return data
}

/**
 * getCreativeRuns retrieves and normalizes a workspace's recent run history. The
 * page argument is unused; pageSize becomes limit, and direct arrays receive a
 * total equal to their length. _page defaults to 1, pageSize defaults to 20.
 */
export async function getCreativeRuns(
  workspaceId: string,
  _page = 1,
  pageSize = 20,
): Promise<CreativeRunsPage> {
  const { data } = await apiClient.get<CreativeRunsPage | CreativeRun[]>('/creative/runs', {
    /**
     * The history endpoint accepts limit; _page remains a compatibility argument for
     * callers and does not select another page in this request.
     */
    params: { limit: pageSize },
    headers: { 'X-Creative-Workspace-ID': workspaceId },
  })
  /**
   * The history endpoint can supply a direct array; wrap it with an inferred
   * total so callers consume one page shape.
   */
  if (Array.isArray(data)) {
    return { items: data, total: data.length }
  }
  /**
   * Keep history rendering usable when items or total are absent by deriving a
   * collection and its count from the available payload.
   */
  const items = Array.isArray(data?.items) ? data.items : []
  return { items, total: typeof data?.total === 'number' ? data.total : items.length }
}

/**
 * getCreativeActiveRuns retrieves a cursor page of active runs for one
 * workspace. Pass the returned cursor unchanged for the next request; missing
 * item, cursor and continuation fields become empty defaults. Limit defaults to
 * 100.
 */
export async function getCreativeActiveRuns(
  workspaceId: string,
  cursor?: string,
  limit = 100,
): Promise<CreativeActiveRunsPage> {
  const { data } = await apiClient.get<CreativeActiveRunsPage>('/creative/runs/active', {
    params: { limit, ...(cursor ? { cursor } : {}) },
    headers: { 'X-Creative-Workspace-ID': workspaceId },
  })
  /**
   * Normalize missing active-page fields together so polling sees an empty page,
   * empty cursor and no continuation when data is incomplete.
   */
  return {
    items: Array.isArray(data?.items) ? data.items : [],
    next_cursor: typeof data?.next_cursor === 'string' ? data.next_cursor : '',
    has_more: data?.has_more === true,
  }
}

/**
 * getCreativeRun retrieves one workspace-owned run including its output
 * metadata.
 */
export async function getCreativeRun(id: string, workspaceId: string): Promise<CreativeRun> {
  const { data } = await apiClient.get<CreativeRun>(`/creative/runs/${encodeURIComponent(id)}`, {
    headers: { 'X-Creative-Workspace-ID': workspaceId },
  })
  return data
}

/**
 * getCreativeRunOutputContent retrieves creative run output content and returns
 * the server response data. The request uses the 300000 timeout budget; the
 * returned data is a Blob for download consumers.
 */
export async function getCreativeRunOutputContent(
  runId: string,
  index: number,
  workspaceId: string,
): Promise<Blob> {
  const { data } = await apiClient.get<Blob>(
    `/creative/runs/${encodeURIComponent(runId)}/outputs/${encodeURIComponent(String(index))}/content`,
    { responseType: 'blob', timeout: 300000, headers: { 'X-Creative-Workspace-ID': workspaceId } },
  )
  return data
}

/**
 * ackCreativeRunOutput confirms that the caller has durably saved an output locally.
 * Persist the asset before calling: the server records acknowledgment and may
 * remove its transient bytes afterward. The workspace identifier binds the request
 * to the owning workspace, and no response body is returned.
 */
export async function ackCreativeRunOutput(
  runId: string,
  index: number,
  workspaceId: string,
): Promise<void> {
  await apiClient.post(
    `/creative/runs/${encodeURIComponent(runId)}/outputs/${encodeURIComponent(String(index))}/ack`,
    undefined,
    { headers: { 'X-Creative-Workspace-ID': workspaceId } },
  )
}
