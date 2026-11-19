import type { BillingSettings } from './pricing.types'

import { apiClient } from '../client'

/**
 * pricing.ts reads editable price configurations and the default model price
 * catalog. Token prices use USD per token, while request and media modes use
 * unit charges. Nullable overrides retain bucket-specific inheritance rules;
 * model lookup, paginated default browsing and remote refresh use separate
 * endpoints and request options.
 */

/**
 * BillingMode selects token, per-request, image or video calculation for a
 * pricing entry.
 */
export type BillingMode = 'token' | 'per_request' | 'image' | 'video'

/**
 * PricingInterval describes a token interval or a named image/video price tier.
 * Token modes use min_tokens and max_tokens, while image and video consumers use
 * tier_label to identify a size tier.
 */
export interface PricingInterval {
  id?: number // Record identifier value.
  min_tokens: number // Lower token bound.
  /**
   * Null represents an unbounded upper token interval; image and video tier
   * selection uses tier_label rather than token interval endpoints.
   */
  max_tokens: number | null // Upper token bound.
  /**
   * Image and video pricing use this named size tier, while token interval
   * selection uses the numeric token boundaries.
   */
  tier_label: string // Named price tier.
  input_price: number | null // USD per input token.
  output_price: number | null // USD per output token.
  cache_write_price: number | null // USD per uniform cache-write token.
  cache_write_5m_price?: number | null // USD per 5m cache-write token.
  /**
   * In user token pricing, null or omission lets a supplied cache_write_price
   * also price the hourly bucket; an explicit hourly value takes precedence.
   */
  cache_write_1h_price?: number | null // USD per hourly cache-write token.
  cache_write_30m_price?: number | null // USD per 30m cache-write token.
  cache_write_12h_price?: number | null // USD per 12h cache-write token.
  cache_read_price: number | null // USD per cache-read token.
  input_multiplier: number | null // Input price multiplier.
  output_multiplier: number | null // Output price multiplier.
  cache_write_multiplier: number | null // Cache write multiplier.
  cache_read_multiplier: number | null // Cache read multiplier.
  per_request_price: number | null // USD per request or media unit.
  sort_order: number // Display ordering position.
}

/**
 * TimePricingPeriod defines a start time, end time and multiplier within a
 * time-pricing schedule.
 */
export interface TimePricingPeriod {
  start_time: string // Requested interval start.
  end_time: string // Requested interval end.
  multiplier: number // Applied price multiplier.
}

/**
 * TimePricingConfig applies periods in its configured timezone with an optional
 * weekdays-only restriction.
 */
export interface TimePricingConfig {
  timezone: string // IANA timezone name.
  weekdays_only?: boolean // Configured weekdays only.
  periods: TimePricingPeriod[] // Time-pricing schedule entries.
}

/**
 * ModelPricingEntry assigns price overrides and multipliers to selected model
 * identifiers. Each nullable override has its own inheritance or default rule,
 * while intervals and time_pricing retain separate tier and time-window rules.
 */
export interface ModelPricingEntry {
  // reference_model selects another model in this profile. Supplied targets require
  // empty price overrides; omission retains custom pricing and its ordinary defaults.
  reference_model?: string // Same-profile price reference.
  id?: number // Record identifier value.
  models: string[] // Model identifier list.
  billing_mode: BillingMode // Selected billing calculation.
  /**
   * Null or omission applies no additional amount scaling after user price
   * overrides; a supplied multiplier scales the resolved amounts.
   */
  price_multiplier?: number | null // Recorded price multiplier.
  /**
   * fast_mode_multiplier and fast_multiplier are legacy and current Fast
   * inputs. HTTP conversion prefers a supplied fast_multiplier; when both are
   * null or omitted, user token pricing retains the inherited Fast factor.
   */
  fast_mode_multiplier?: number | null // Legacy Fast multiplier.
  fast_multiplier?: number | null // Preferred Fast multiplier.
  /**
   * Null or omission retains the inherited Flex factor; a supplied positive
   * value overrides that factor for user token pricing.
   */
  flex_multiplier?: number | null // Configured Flex multiplier.
  /**
   * Null or omission retains the inherited reasoning factor; a supplied value
   * applies when the final forwarded reasoning effort is max.
   */
  max_reasoning_effort_multiplier?: number | null // Max reasoning effort multiplier.
  input_price: number | null // USD per input token.
  output_price: number | null // USD per output token.
  cache_write_price: number | null // USD per uniform cache-write token.
  cache_write_5m_price?: number | null // USD per 5m cache-write token.
  /**
   * In user token pricing, null or omission lets a supplied cache_write_price
   * also price the hourly bucket; an explicit hourly value takes precedence.
   */
  cache_write_1h_price?: number | null // USD per hourly cache-write token.
  cache_write_30m_price?: number | null // USD per 30m cache-write token.
  cache_write_12h_price?: number | null // USD per 12h cache-write token.
  cache_read_price: number | null // USD per cache-read token.
  /**
   * User token pricing uses the selected text-input rate when this image-input
   * override is null, omitted or zero.
   */
  image_input_price: number | null // USD per image-input token.
  /**
   * User token pricing charges image-output tokens at zero when this override
   * is null, omitted or zero, including when catalog output pricing exists.
   */
  image_output_price: number | null // USD per image-output token.
  per_request_price: number | null // USD per request or media unit.
  intervals: PricingInterval[] // Configured price intervals.
  /**
   * A configured schedule applies its timezone and periods to the entry; null
   * leaves time-based adjustments unconfigured for this price entry.
   */
  time_pricing?: TimePricingConfig | null // Recorded time pricing.
}

/**
 * ProviderStatsPricingRule assigns model price entries to selected groups and
 * providers for provider-cost statistics.
 */
export interface ProviderStatsPricingRule {
  id?: number // Record identifier value.
  name: string // Human-readable display name.
  group_ids: number[] // Recorded group ids.
  provider_ids: number[] // Recorded provider ids.
  pricing: ModelPricingEntry[] // Configured model prices.
}

/**
 * PricingConfig combines a shared billing configuration with model prices, group
 * bindings and provider-statistics rules.
 */
export interface PricingConfig extends BillingSettings {
  id: number // Record identifier value.
  name: string // Human-readable display name.
  description: string // Human-readable record description.
  status: string // Current server-reported status.
  billing_model_source: string // "requested" | "group_mapped" | "upstream"
  group_ids: number[] // Configured group ids.
  model_pricing: ModelPricingEntry[] // Configured model pricing.
  provider_stats_pricing_rules: ProviderStatsPricingRule[] // Provider stats pricing rules.
  created_at: string // Record creation timestamp.
  updated_at: string // Last modification timestamp.
}

/**
 * CreatePricingConfigRequest supplies a new shared price configuration and any
 * initial group or model assignments.
 */
export interface CreatePricingConfigRequest extends Partial<BillingSettings> {
  name: string // Human-readable display name.
  description?: string // Human-readable record description.
  group_ids?: number[] // Requested group ids.
  model_pricing?: ModelPricingEntry[] // Requested model pricing.
  billing_model_source?: string // Billing model source.
  provider_stats_pricing_rules?: ProviderStatsPricingRule[] // Provider stats pricing rules.
}

/**
 * UpdatePricingConfigRequest supplies selected edits to a shared price
 * configuration and its group or model assignments.
 */
export interface UpdatePricingConfigRequest extends Partial<BillingSettings> {
  name?: string // Human-readable display name.
  description?: string // Human-readable record description.
  status?: string // Current server-reported status.
  group_ids?: number[] // Requested group ids.
  model_pricing?: ModelPricingEntry[] // Requested model pricing.
  billing_model_source?: string // Billing model source.
  provider_stats_pricing_rules?: ProviderStatsPricingRule[] // Provider stats pricing rules.
}

/**
 * PaginatedResponse pairs price-configuration entries with their total count for
 * administrator pagination.
 */
interface PaginatedResponse<T> {
  items: T[] // Returned collection entries.
  total: number // Total matching records.
}

/**
 * list retrieves administrator pricing configurations with pagination and
 * optional filters. Page defaults to 1, pageSize defaults to 20; the caller may
 * cancel through the supplied AbortSignal.
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    status?: string // Current server-reported status.
    search?: string // Free-text search query.
    sort_by?: string // Requested sort column.
    sort_order?: 'asc' | 'desc' // Requested ordering direction.
  },
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<PaginatedResponse<PricingConfig>> {
  const { data } = await apiClient.get<PaginatedResponse<PricingConfig>>('/admin/pricing/configs', {
    params: {
      page,
      page_size: pageSize,
      ...filters,
    },
    signal: options?.signal,
  })
  return data
}

/**
 * getById retrieves one administrator pricing configuration by its numeric
 * identifier.
 */
export async function getById(id: number): Promise<PricingConfig> {
  const { data } = await apiClient.get<PricingConfig>(`/admin/pricing/configs/${id}`)
  return data
}

/**
 * create submits a pricing configuration and returns the created record.
 */
export async function create(req: CreatePricingConfigRequest): Promise<PricingConfig> {
  const { data } = await apiClient.post<PricingConfig>('/admin/pricing/configs', req)
  return data
}

/**
 * update submits pricing configuration edits and returns the updated record.
 */
export async function update(id: number, req: UpdatePricingConfigRequest): Promise<PricingConfig> {
  const { data } = await apiClient.put<PricingConfig>(`/admin/pricing/configs/${id}`, req)
  return data
}

/**
 * remove requests deletion of the selected administrator pricing configuration.
 */
export async function remove(id: number): Promise<void> {
  await apiClient.delete(`/admin/pricing/configs/${id}`)
}

/**
 * ModelDefaultPricing reports catalog lookup availability and optional default
 * prices for one requested model.
 */
export interface ModelDefaultPricing {
  found: boolean // Catalog lookup availability.
  input_price?: number // USD per input token.
  output_price?: number // USD per output token.
  cache_write_price?: number // USD per cache-write token.
  cache_write_5m_price?: number | null // USD per 5m cache-write token.
  cache_write_1h_price?: number | null // USD per hourly cache-write token.
  cache_write_30m_price?: number | null // USD per 30m cache-write token.
  cache_write_12h_price?: number | null // USD per 12h cache-write token.
  cache_read_price?: number // USD per cache-read token.
  max_reasoning_effort_multiplier?: number | null // Max reasoning effort multiplier.
  image_input_price?: number // USD per image-input token.
  image_output_price?: number // USD per image-output token.
}

/**
 * getModelDefaultPricing retrieves model default pricing and returns the server
 * response data.
 */
export async function getModelDefaultPricing(model: string): Promise<ModelDefaultPricing> {
  const { data } = await apiClient.get<ModelDefaultPricing>('/admin/pricing/defaults/model', {
    params: { model },
  })
  return data
}

/**
 * pricingAPI groups shared pricing configurations and default price queries for
 * callers using the module's named or default API object.
 */
const pricingAPI = { list, getById, create, update, remove, getModelDefaultPricing }
export default pricingAPI

/**
 * DefaultPriceValue pairs a catalog price key with its amount and reported
 * display unit.
 */
export interface DefaultPriceValue {
  key: string // Stable entry key.
  /**
   * Null indicates the catalog lacks this price amount; consumers retain that
   * distinction from an explicit zero price.
   */
  value: number | null // Represented entry value.
  unit: string // Reported price unit.
}

/**
 * DefaultModelPrice describes a catalog model's pricing status and available
 * price values. context_intervals and long-context fields retain tier metadata
 * for consumers that display context-dependent prices.
 */
export interface DefaultModelPrice {
  source?: string // Recorded observation source.
  price_sources?: Record<string, string> // Recorded price sources.
  context_intervals?: {
    min_tokens: number // Lower token bound.
    max_tokens: number | null // Upper token bound.
    prices: DefaultPriceValue[] // Catalog price entries.
  }[] // Recorded context intervals.
  model: string // Model identifier value.
  platform: string // Provider platform identifier.
  billing_mode: string // Selected billing calculation.
  price_status: 'priced' | 'unpriced' // Recorded price status.
  prices: DefaultPriceValue[] // Catalog price entries.
  long_context_threshold?: number // Long context threshold.
  long_context_threshold_inclusive?: boolean // Long context threshold inclusive.
}
/**
 * listDefaultPricing retrieves default pricing and returns the server response
 * data. The caller may cancel through the supplied AbortSignal. The function
 * returns the data portion of the completed response.
 */
export async function listDefaultPricing(
  params: {
    page: number // Requested page number.
    page_size: number // Requested page size.
    platform?: string // Provider platform identifier.
    search?: string // Free-text search query.
    billing_mode?: string // Selected billing calculation.
  },
  signal?: AbortSignal,
): Promise<
  PaginatedResponse<DefaultModelPrice> & {
    last_updated: string // Recorded last updated.
    platforms?: string[] // Supported provider platforms.
    version?: string // Reported version identifier.
    last_error?: string // Recorded last error.
  }
> {
  const { data } = await apiClient.get('/admin/pricing/defaults', { params, signal })
  return data
}

/**
 * updateDefaultPricing requests a catalog refresh with a two-minute request
 * budget. Remote hash checks and catalog downloads can outlast the shared
 * client's usual timeout; the caller may cancel with its AbortSignal.
 */
export async function updateDefaultPricing(signal?: AbortSignal): Promise<void> {
  await apiClient.post('/admin/pricing/defaults/update', undefined, { signal, timeout: 120_000 })
}
