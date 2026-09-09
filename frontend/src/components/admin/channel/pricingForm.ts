import type { ChannelModelPricing } from '@/api/admin/channels'
import {
  apiIntervalsToForm, apiTimePricingToForm, findModelConflict, formIntervalsToAPI,
  formTimePricingToAPI, hasExplicitPricing, isValidPositiveMultiplier, mTokToPerToken,
  perTokenToMTok, toNullableNumber, validateIntervals, validateTimePricing,
  type PricingFormEntry,
} from './types'

type TranslateFn = (key: string, params?: Record<string, unknown>) => string

// 分组和渠道共用完整价卡转换，避免新增字段只在其中一个管理页面保存。
export function pricingEntryFromAPI(entry: ChannelModelPricing): PricingFormEntry {
  return {
    models: [...(entry.models || [])],
    billing_mode: entry.billing_mode || 'token',
    price_multiplier: entry.price_multiplier ?? null,
    fast_mode_multiplier: entry.fast_multiplier == null ? entry.fast_mode_multiplier ?? null : null,
    fast_multiplier: entry.fast_multiplier ?? null,
    flex_multiplier: entry.flex_multiplier ?? null,
    max_reasoning_effort_multiplier: entry.max_reasoning_effort_multiplier ?? null,
    input_price: perTokenToMTok(entry.input_price),
    output_price: perTokenToMTok(entry.output_price),
    cache_write_price: perTokenToMTok(entry.cache_write_price),
    cache_write_1h_price: perTokenToMTok(entry.cache_write_1h_price),
    cache_read_price: perTokenToMTok(entry.cache_read_price),
    image_input_price: perTokenToMTok(entry.image_input_price),
    image_output_price: perTokenToMTok(entry.image_output_price),
    per_request_price: entry.per_request_price,
    intervals: apiIntervalsToForm(entry.intervals || []),
    time_pricing: apiTimePricingToForm(entry.time_pricing),
  }
}

// 单价按百万 token 展示、按单 token 存储；区间倍率和服务层级倍率保持原单位。
export function pricingEntryToAPI(entry: PricingFormEntry, platform: string): ChannelModelPricing {
  return {
    platform,
    models: entry.models.map(model => model.trim()),
    billing_mode: entry.billing_mode,
    price_multiplier: toNullableNumber(entry.price_multiplier),
    fast_mode_multiplier: toNullableNumber(entry.fast_mode_multiplier),
    fast_multiplier: toNullableNumber(entry.fast_multiplier),
    flex_multiplier: toNullableNumber(entry.flex_multiplier),
    max_reasoning_effort_multiplier: toNullableNumber(entry.max_reasoning_effort_multiplier),
    input_price: mTokToPerToken(entry.input_price),
    output_price: mTokToPerToken(entry.output_price),
    cache_write_price: mTokToPerToken(entry.cache_write_price),
    cache_write_1h_price: mTokToPerToken(entry.cache_write_1h_price),
    cache_read_price: mTokToPerToken(entry.cache_read_price),
    image_input_price: mTokToPerToken(entry.image_input_price),
    image_output_price: mTokToPerToken(entry.image_output_price),
    per_request_price: toNullableNumber(entry.per_request_price),
    intervals: formIntervalsToAPI(entry.intervals || []),
    time_pricing: formTimePricingToAPI(entry.time_pricing),
  }
}

// 校验使用与后端定价缓存相同的 Claude 点号归一化，不影响模型映射的匹配语义。
export function validatePricingForm(entries: PricingFormEntry[], t: TranslateFn): string | null {
  const configured = entries.filter(entry => entry.models.length > 0)
  const models = configured.flatMap(entry => entry.models.map(model => {
    const normalized = model.trim().toLowerCase()
    return normalized.startsWith('claude-') ? normalized.replace(/\./g, '-') : normalized
  }))
  const conflict = findModelConflict(models)
  if (conflict) return t('admin.channels.modelConflict', { model1: conflict[0], model2: conflict[1] })
  for (const entry of configured) {
    const error = validatePricingEntry(entry, t)
    if (error) return `${entry.models.join(', ')}: ${error}`
  }
  return null
}

function validatePricingEntry(entry: PricingFormEntry, t: TranslateFn): string | null {
  const prices = [entry.price_multiplier, entry.fast_mode_multiplier, entry.input_price,
    entry.output_price, entry.cache_write_price, entry.cache_write_1h_price,
    entry.cache_read_price, entry.image_input_price, entry.image_output_price, entry.per_request_price]
  if (prices.some(value => value != null && value !== '' && (!Number.isFinite(Number(value)) || Number(value) < 0))) {
    return t('admin.channels.form.invalidPrice')
  }
  const explicitPrice = hasExplicitPricing({ ...entry, intervals: entry.intervals.map(interval => ({
    ...interval, input_multiplier: null, output_multiplier: null, cache_write_multiplier: null, cache_read_multiplier: null,
  })) })
  const models = entry.models.join(', ')
  if (entry.billing_mode !== 'token' && entry.per_request_price == null && entry.intervals.length === 0) {
    return t('admin.channels.form.perRequestPriceRequired')
  }
  if (entry.price_multiplier != null && entry.price_multiplier !== '' && !explicitPrice) {
    return t('admin.channels.form.priceMultiplierRequiresPrice', { models })
  }
  if (entry.fast_mode_multiplier != null && entry.fast_mode_multiplier !== '' && !explicitPrice) {
    return t('admin.channels.form.fastModeMultiplierRequiresPrice', { models })
  }
  if (![entry.fast_multiplier, entry.flex_multiplier, entry.max_reasoning_effort_multiplier].every(isValidPositiveMultiplier)) {
    return t('admin.channels.form.tierMultiplierMustBePositive', { models })
  }
  return validateIntervals(entry.intervals, entry.billing_mode, t) || validateTimePricing(entry.time_pricing, t)
}
