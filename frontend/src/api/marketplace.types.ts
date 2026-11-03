import type { ModelAttributePresentation } from '@/api/modelAttributes.types'
import type { ProtocolID } from '@/api/protocols.types'
import type { ModelModality } from '@/api/models.types'

// marketplace.types.ts describes visible groups, model capabilities, availability
// observations, and quoted prices. Price records distinguish unpriced models from free
// amounts, and model protocol lists describe the client interfaces currently requestable
// through each advertised group.

// MarketplacePricingMode identifies the unit selected for a public model quote.
export type MarketplacePricingMode = 'token' | 'image' | 'video' | 'unknown'

// MarketplacePriceStatus distinguishes an available public price from an unpriced model.
export type MarketplacePriceStatus = 'priced' | 'unpriced'

// MarketplacePricingInterval supplies token prices within one context-length pricing
// interval.
export interface MarketplacePricingInterval {
  min_tokens: number // Minimum context token count.
  max_tokens?: number | null // Maximum context token count.
  input_price_per_token?: number // Input price per token.
  image_input_price_per_token?: number // Image input price per token.
  output_price_per_token?: number // Output price per token.
  cache_write_price_per_token?: number // Cache write price per token.
  cache_write_1h_price_per_token?: number // Cache write 1h price per token.
  cache_read_price_per_token?: number // Cache read price per token.
  image_output_price_per_token?: number // Image output price per token.
  fast_input_price_per_token?: number // Fast input price per token.
  fast_image_input_price_per_token?: number // Fast image input price per token.
  fast_output_price_per_token?: number // Fast output price per token.
  fast_cache_write_price_per_token?: number // Fast cache write price per token.
  fast_cache_write_1h_price_per_token?: number // Fast cache write 1h price per token.
  fast_cache_read_price_per_token?: number // Fast cache read price per token.
  fast_image_output_price_per_token?: number // Fast image output price per token.
}

// MarketplaceTimePricing applies configured time multipliers; outside periods pricing is
// 1x, and end time 00:00 denotes 24:00.
export interface MarketplaceTimePricing {
  timezone: string // IANA timezone name.
  weekdays_only: boolean // Weekday pricing restriction.
  periods: Array<{
    start_time: string // Local interval start.
    end_time: string // Local interval end.
    multiplier: number // Applied price multiplier.
  }> // Configured pricing intervals.
}

// MarketplaceModelPricing carries group-adjusted prices and optional token pricing
// modifiers.
export interface MarketplaceModelPricing {
  // video_prices includes quoted resolutions only; zero declares free video seconds. Each
  // amount already includes the group multiplier used by this public quote.
  video_prices?: Record<string, number> // Prices by video resolution.
  // video_price_per_second is the explicit default for unlisted resolutions; absence
  // means unavailable and zero remains a valid free default.
  video_price_per_second?: number // Video price per second.
  pricing_mode: MarketplacePricingMode // Public quote billing unit.
  price_status: MarketplacePriceStatus // Public quote availability.
  input_price_per_token?: number // Input price per token.
  image_input_price_per_token?: number // Image input price per token.
  output_price_per_token?: number // Output price per token.
  cache_write_price_per_token?: number // Cache write price per token.
  cache_write_1h_price_per_token?: number // Cache write 1h price per token.
  cache_read_price_per_token?: number // Cache read price per token.
  image_output_price_per_token?: number // Image output price per token.
  fast_input_price_per_token?: number // Fast input price per token.
  fast_image_input_price_per_token?: number // Fast image input price per token.
  fast_output_price_per_token?: number // Fast output price per token.
  fast_cache_write_price_per_token?: number // Fast cache write price per token.
  fast_cache_write_1h_price_per_token?: number // Fast cache write 1h price per token.
  fast_cache_read_price_per_token?: number // Fast cache read price per token.
  fast_image_output_price_per_token?: number // Fast image output price per token.
  context_intervals?: MarketplacePricingInterval[] // Context-length price intervals.
  // Multiplier applied to every token unit price when the final reasoning effort is max.
  max_reasoning_effort_multiplier?: number // Maximum reasoning effort multiplier.
  // Request-time token price modifiers; the unit prices above represent the unmodified 1x
  // amounts.
  time_pricing?: MarketplaceTimePricing // Time-based token pricing.
  image_price_1k?: number // Image price 1k.
  image_price_2k?: number // Image price 2k.
  image_price_4k?: number // Image price 4k.
}

// MarketplaceModel describes a requestable model and its public pricing and capabilities.
export interface MarketplaceModel {
  // Attributes describe the available range across all locally eligible routes.
  attributes?: ModelAttributePresentation // Capabilities across eligible routes.
  // Protocol attributes reuse those resolved routes for each client protocol.
  protocol_attributes?: Partial<Record<ProtocolID, ModelAttributePresentation>> // Capabilities by client protocol.
  id: string // Record string identifier.
  display_name: string // Human-readable display name.
  pricing: MarketplaceModelPricing // Public model prices.
  // input_modalities and output_modalities come from pricing metadata. When either
  // side lacks valid nonempty data, resolveModelCapabilities uses pricing and model
  // identifier rules to determine the marketplace display labels.
  input_modalities?: ModelModality[] // Accepted input media.
  output_modalities?: ModelModality[] // Produced output media.
  // Client protocols that this group makes requestable for the model.
  protocols?: ProtocolID[] // Requestable client protocols.
  // The subset of protocols that reaches an upstream without protocol conversion for this
  // model.
  native_protocols?: ProtocolID[] // Direct upstream protocol subset.
}

// MarketplaceGroupCapacity carries aggregate group load counters exposed to marketplace
// consumers.
export interface MarketplaceGroupCapacity {
  concurrency_used: number // Current concurrent requests.
  concurrency_max: number // Aggregate concurrent request ceiling.
  sessions_used: number // Current active sessions.
  sessions_max: number // Aggregate session ceiling.
  rpm_used: number // Current minute request count.
  rpm_max: number // Aggregate per-minute request ceiling.
}

// MarketplaceGroupAvailabilityDay summarizes probe outcomes within one daily availability
// bucket.
export interface MarketplaceGroupAvailabilityDay {
  date: string // Reporting calendar date.
  success_count: number // Successful probe count.
  total_count: number // Total observation count.
  availability_rate?: number | null // Observed probe success rate.
}

// MarketplaceGroupAvailability summarizes recent group probe outcomes and daily
// availability history.
export interface MarketplaceGroupAvailability {
  window_days: number // Availability reporting window days.
  bucket_minutes?: number // Availability bucket duration minutes.
  success_count: number // Successful probe count.
  total_count: number // Total observation count.
  availability_rate?: number | null // Observed probe success rate.
  last_status?: string // Latest probe status.
  last_checked_at?: string | null // Last checked timestamp.
  days: MarketplaceGroupAvailabilityDay[] // Daily probe outcome buckets.
}

// MarketplaceGroup describes a visible routing group and its requestable model catalog.
export interface MarketplaceGroup {
  search_terms?: string[] // Additional searchable text.
  id: number // Record numeric identifier.
  // Missing flags represent public groups when consuming older marketplace responses without this field.
  is_exclusive?: boolean // Requires explicit user authorization.
  name: string // Record display name.
  description: string // Human-readable record description.
  display_brand: string // Displayed provider brand.
  sort_order: number // Display sorting position.
  rate_multiplier: number // Applied billing multiplier.
  // hide_max_discount and hide_rate_multiplier independently suppress group header tags.
  // Absent flags keep both tags eligible for display when reading older server responses.
  hide_max_discount?: boolean // Hide discount header tag.
  hide_rate_multiplier?: boolean // Hide multiplier header tag.
  official_price_ratio?: number // Official price ratio.
  official_price_rmb_equivalent?: number // Official price rmb equivalent.
  capacity?: MarketplaceGroupCapacity // Aggregated group load.
  availability?: MarketplaceGroupAvailability // Recent availability observations.
  model_count: number // Requestable model count.
  models: MarketplaceModel[] // Requestable model catalog entries.
}

// MarketplaceStats reports public token usage totals and the registered user count.
export interface MarketplaceStats {
  today_tokens: number // Tokens consumed today.
  total_tokens: number // Total consumed tokens.
  total_users: number // Registered account count.
}
