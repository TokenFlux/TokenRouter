/**
 * 管理员价格配置接口
 */

import { apiClient } from '../client'

export type BillingMode = 'token' | 'per_request' | 'image' | 'video'

export interface PricingInterval {
  id?: number
  min_tokens: number
  max_tokens: number | null
  tier_label: string
  input_price: number | null
  output_price: number | null
  cache_write_price: number | null
  cache_write_1h_price?: number | null
  cache_read_price: number | null
  input_multiplier: number | null
  output_multiplier: number | null
  cache_write_multiplier: number | null
  cache_read_multiplier: number | null
  per_request_price: number | null
  sort_order: number
}

export interface TimePricingPeriod {
  start_time: string
  end_time: string
  multiplier: number
}

export interface TimePricingConfig {
  timezone: string
  weekdays_only?: boolean
  periods: TimePricingPeriod[]
}

export interface ModelPricingEntry {
  id?: number
  platform: string
  models: string[]
  billing_mode: BillingMode
  // 可空表示完全沿用现有定价，不隐式写入 1 倍。
  price_multiplier?: number | null
  // 仅用于 OpenAI token 定价；可空表示沿用模型默认 Fast 定价。
  fast_mode_multiplier?: number | null
  // 通用服务层级倍率；为空表示沿用模型目录或官方默认倍率。
  fast_multiplier?: number | null
  flex_multiplier?: number | null
  max_reasoning_effort_multiplier?: number | null
  input_price: number | null
  output_price: number | null
  cache_write_price: number | null
  cache_write_1h_price?: number | null
  cache_read_price: number | null
  image_input_price: number | null
  image_output_price: number | null
  per_request_price: number | null
  intervals: PricingInterval[]
  time_pricing?: TimePricingConfig | null
}

export interface AccountStatsPricingRule {
  id?: number
  name: string
  group_ids: number[]
  account_ids: number[]
  pricing: ModelPricingEntry[]
}

export interface PricingConfig {
  id: number
  name: string
  description: string
  status: string
  billing_model_source: string // "requested" | "group_mapped" | "upstream"
  group_ids: number[]
  model_pricing: ModelPricingEntry[]
  apply_pricing_to_account_stats: boolean
  account_stats_pricing_rules: AccountStatsPricingRule[]
  created_at: string
  updated_at: string
}

export interface CreatePricingConfigRequest {
  name: string
  description?: string
  group_ids?: number[]
  model_pricing?: ModelPricingEntry[]
  billing_model_source?: string
  apply_pricing_to_account_stats?: boolean
  account_stats_pricing_rules?: AccountStatsPricingRule[]
}

export interface UpdatePricingConfigRequest {
  name?: string
  description?: string
  status?: string
  group_ids?: number[]
  model_pricing?: ModelPricingEntry[]
  billing_model_source?: string
  apply_pricing_to_account_stats?: boolean
  account_stats_pricing_rules?: AccountStatsPricingRule[]
}

interface PaginatedResponse<T> {
  items: T[]
  total: number
}

/**
 * 分页查询价格配置
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    status?: string
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: { signal?: AbortSignal }
): Promise<PaginatedResponse<PricingConfig>> {
  const { data } = await apiClient.get<PaginatedResponse<PricingConfig>>('/admin/pricing/configs', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

/**
 * 按 ID 查询价格配置
 */
export async function getById(id: number): Promise<PricingConfig> {
  const { data } = await apiClient.get<PricingConfig>(`/admin/pricing/configs/${id}`)
  return data
}

/**
 * 创建价格配置
 */
export async function create(req: CreatePricingConfigRequest): Promise<PricingConfig> {
  const { data } = await apiClient.post<PricingConfig>('/admin/pricing/configs', req)
  return data
}

/**
 * 更新价格配置
 */
export async function update(id: number, req: UpdatePricingConfigRequest): Promise<PricingConfig> {
  const { data } = await apiClient.put<PricingConfig>(`/admin/pricing/configs/${id}`, req)
  return data
}

/**
 * 删除价格配置
 */
export async function remove(id: number): Promise<void> {
  await apiClient.delete(`/admin/pricing/configs/${id}`)
}

export interface ModelDefaultPricing {
  found: boolean
  input_price?: number    // per-token price
  output_price?: number
  cache_write_price?: number
  cache_write_1h_price?: number | null
  cache_read_price?: number
  max_reasoning_effort_multiplier?: number | null
  image_input_price?: number
  image_output_price?: number
}

export async function getModelDefaultPricing(model: string, platform?: string): Promise<ModelDefaultPricing> {
  const { data } = await apiClient.get<ModelDefaultPricing>('/admin/pricing/defaults/model', {
    params: { model, platform }
  })
  return data
}

export interface SyncPricingModelsResult {
  models: string[]
}

/**
 * 从 LiteLLM 定价目录获取指定平台的最新模型名
 */
export async function syncPricingModels(platform: string): Promise<SyncPricingModelsResult> {
  const { data } = await apiClient.get<SyncPricingModelsResult>('/admin/pricing/defaults/models', {
    params: { platform }
  })
  return data
}

const pricingAPI = { list, getById, create, update, remove, getModelDefaultPricing, syncPricingModels }
export default pricingAPI

export interface DefaultPriceValue {
  key: string
  value: number | null
  unit: string
}
export interface DefaultModelPrice {
  model: string
  platform: string
  billing_mode: string
  price_status: 'priced' | 'unpriced'
  prices: DefaultPriceValue[]
  long_context_threshold?: number
  long_context_threshold_inclusive?: boolean
}
export async function listDefaultPricing(params: { page: number; page_size: number; platform?: string; search?: string; billing_mode?: string }, signal?: AbortSignal): Promise<PaginatedResponse<DefaultModelPrice> & { last_updated: string; platforms?: string[] }> {
  const { data } = await apiClient.get('/admin/pricing/defaults', { params, signal })
  return data
}
