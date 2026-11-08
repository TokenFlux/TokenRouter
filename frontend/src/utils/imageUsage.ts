import type { UsageLog } from '@/api/usage.types'

/**
 * imageUsage.ts separates image and text token counts and formats recorded image sizes
 * for usage displays. Token subtraction clamps negative remainders to zero, while size
 * formatting distinguishes missing billing dimensions from unrecognized historical
 * labels. Output breakdowns show positive counts for known size tiers in a fixed order.
 *
 * Image size source metadata is interpreted together with the recorded billing size. A
 * recognized source receives its own translated label; an unknown source with a size is
 * displayed as legacy data, and an unknown source without a size is displayed as
 * missing. These presentation rules keep historical records readable when individual
 * metadata fields are absent.
 */

/**
 * Translate resolves an image usage label in the caller locale.
 */
type Translate = (key: string) => string

/**
 * knownImageSizeSources and knownImageBillingSizes enumerate values with dedicated
 * presentation, separating recognized metadata from legacy or absent size information.
 */
const knownImageSizeSources = new Set(['output', 'input', 'default', 'legacy'])
const knownImageBillingSizes = new Set(['1K', '2K', '4K', 'mixed'])

/**
 * ImageOutputTokenRow supplies total and image output counts for token separation.
 */
type ImageOutputTokenRow = Pick<UsageLog, 'output_tokens' | 'image_output_tokens'>
/**
 * ImageOutputCostRow supplies the recorded charge attributed to image output.
 */
type ImageOutputCostRow = Pick<UsageLog, 'image_output_cost'>

/**
 * hasImageOutputTokens reports a positive recorded image output token count. Missing
 * rows and nullish counts use zero, so the display can hide the image token section
 * when no positive count exists.
 */
export const hasImageOutputTokens = (row: ImageOutputTokenRow | null | undefined): boolean =>
  (row?.image_output_tokens ?? 0) > 0

/**
 * textOutputTokens returns the nonnegative text remainder of the recorded total
 * output tokens. Total and image counts overlap because the image count
 * represents a portion of that total, so subtraction is needed before displaying
 * separate text and image quantities.
 *
 * Each missing count is treated as zero. When the image portion exceeds the
 * recorded total, the remainder clamps to zero, keeping inconsistent or partial
 * records from producing negative text counts. The function returns the computed
 * count without rounding.
 */
export const textOutputTokens = (row: ImageOutputTokenRow | null | undefined): number =>
  /**
   * Clamp inconsistent totals so the text output count cannot become negative.
   */
  Math.max(0, (row?.output_tokens ?? 0) - (row?.image_output_tokens ?? 0))

/**
 * hasImageOutputCost reports a positive recorded image output charge. Missing rows and
 * nullish charges use zero, so zero and negative charges do not enable the image-cost
 * section.
 */
export const hasImageOutputCost = (row: ImageOutputCostRow | null | undefined): boolean =>
  (row?.image_output_cost ?? 0) > 0

/**
 * ImageInputTokenRow supplies total and image input counts for token separation.
 */
type ImageInputTokenRow = Pick<
  UsageLog,
  'input_tokens' | 'image_input_tokens' | 'image_cache_read_tokens'
>
/**
 * ImageInputCostRow supplies the recorded charge attributed to image input.
 */
type ImageInputCostRow = Pick<UsageLog, 'image_input_cost'>

/**
 * uncachedImageInputTokens separates explicitly reported cached image tokens from
 * the upstream image-input total. Missing subdivisions remain zero; malformed
 * negative remainders clamp to zero before usage tables calculate unit prices.
 */
export const uncachedImageInputTokens = (row: ImageInputTokenRow | null | undefined): number =>
  Math.max(0, (row?.image_input_tokens ?? 0) - (row?.image_cache_read_tokens ?? 0))

/**
 * hasImageInputTokens reports a positive uncached image input token count. Missing rows
 * and nullish counts use zero, allowing consumers to hide the input-image section when
 * its count is absent.
 */
export const hasImageInputTokens = (row: ImageInputTokenRow | null | undefined): boolean =>
  uncachedImageInputTokens(row) > 0

/**
 * textInputTokens returns the nonnegative text remainder of the recorded total
 * input tokens. The uncached image portion belongs to that ordinary input total, so subtraction is needed before displaying
 * separate text and image quantities.
 *
 * Each missing count is treated as zero. When the image portion exceeds the
 * recorded total, the remainder clamps to zero, keeping inconsistent or partial
 * records from producing negative text counts. The function returns the computed
 * count without rounding.
 */
export const textInputTokens = (row: ImageInputTokenRow | null | undefined): number =>
  /**
   * Clamp inconsistent totals so the text input count cannot become negative.
   */
  Math.max(0, (row?.input_tokens ?? 0) - uncachedImageInputTokens(row))

/**
 * hasImageInputCost reports a positive recorded image input charge. Missing rows and
 * nullish charges use zero, so zero and negative charges do not enable the input-image
 * cost section.
 */
export const hasImageInputCost = (row: ImageInputCostRow | null | undefined): boolean =>
  (row?.image_input_cost ?? 0) > 0

/**
 * ImageUsageRow supplies billing, input, output, and provenance details for image
 * sizes.
 */
type ImageUsageRow = Pick<
  UsageLog,
  | 'image_size'
  | 'image_input_size'
  | 'image_output_size'
  | 'image_size_source'
  | 'image_size_breakdown'
>

/**
 * trimmed removes surrounding whitespace from an optional image metadata string.
 * Nullish values become empty text so all image size formatters can apply their own
 * missing-value labels.
 */
const trimmed = (value: string | null | undefined): string => value?.trim() ?? ''

/**
 * formatImageBillingSize renders the recorded billing size or its missing-data label.
 * Known tier values pass through unchanged; other nonempty values receive a translated
 * legacy prefix while retaining their trimmed spelling.
 */
export const formatImageBillingSize = (
  row: ImageUsageRow | null | undefined,
  t: Translate,
): string => {
  const size = trimmed(row?.image_size)
  if (!size) {
    return t('usage.imageSizeNotRecorded')
  }
  if (knownImageBillingSizes.has(size)) {
    return size
  }
  return `${t('usage.imageSizeLegacyUnstandardized')}: ${size}`
}

/**
 * formatImageInputSize returns the trimmed input image dimensions for a usage row.
 * Missing rows and empty metadata use the translated unknown-size label, while nonempty
 * dimensions pass through unchanged.
 */
export const formatImageInputSize = (
  row: ImageUsageRow | null | undefined,
  t: Translate,
): string => {
  const size = trimmed(row?.image_input_size)
  return size || t('usage.imageSizeUnknown')
}

/**
 * formatImageOutputSize returns the trimmed output image dimensions for a usage row.
 * Missing rows and empty metadata use the translated unknown-size label, while nonempty
 * dimensions pass through unchanged.
 */
export const formatImageOutputSize = (
  row: ImageUsageRow | null | undefined,
  t: Translate,
): string => {
  const size = trimmed(row?.image_output_size)
  return size || t('usage.imageSizeUnknown')
}

/**
 * formatImageSizeSource chooses a provenance label using source metadata and the
 * recorded billing size. Known sources are trimmed and lowercased before selecting
 * their translated label.
 *
 * When the source is unrecognized, a nonempty billing size supplies evidence that size
 * data exists, so the label becomes legacy. When both source and billing size are
 * unavailable, the label becomes missing. Input or output dimensions alone do not
 * participate in that distinction.
 */
export const formatImageSizeSource = (
  row: ImageUsageRow | null | undefined,
  t: Translate,
): string => {
  const source = trimmed(row?.image_size_source).toLowerCase()
  if (knownImageSizeSources.has(source)) {
    return t(`usage.imageSizeSource${source.charAt(0).toUpperCase()}${source.slice(1)}`)
  }
  if (trimmed(row?.image_size)) {
    /**
     * An unrecognized provenance value still accompanies recorded billing dimensions
     * here, so label the existing size as legacy metadata.
     */
    return t('usage.imageSizeSourceLegacy')
  }
  return t('usage.imageSizeSourceMissing')
}

/**
 * formatImageSizeBreakdown renders positive image counts in 1K, 2K, then 4K order.
 * Missing, empty, or wholly unrecognized breakdowns produce empty text; zero and
 * negative counts are omitted.
 */
export const formatImageSizeBreakdown = (row: ImageUsageRow | null | undefined): string => {
  const breakdown = row?.image_size_breakdown
  if (!breakdown || Object.keys(breakdown).length === 0) {
    return ''
  }
  return ['1K', '2K', '4K']
    .filter((tier) => (breakdown[tier] ?? 0) > 0)
    .map((tier) => `${tier} x ${breakdown[tier]}`)
    .join(', ')
}
