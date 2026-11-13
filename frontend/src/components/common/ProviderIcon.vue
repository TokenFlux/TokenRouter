<template>
  <GoogleMark
    v-if="brandInfo.key === 'google' && !color"
    :width="size"
    :height="size"
    class="provider-icon"
  />
  <span
    v-else-if="iconInfo?.maskUrl"
    class="inline-block shrink-0"
    :style="{
      width: size,
      height: size,
      backgroundColor: iconColor,
      maskImage: `url(${iconInfo.maskUrl})`,
      maskSize: 'contain',
      maskRepeat: 'no-repeat',
      maskPosition: 'center',
    }"
    aria-hidden="true"
  />
  <svg
    v-else-if="iconInfo"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    xmlns="http://www.w3.org/2000/svg"
    class="provider-icon"
    :class="!color && brandInfo.key === 'openai' ? 'text-black dark:text-white' : undefined"
    fill="currentColor"
    fill-rule="evenodd"
    aria-hidden="true"
  >
    <path v-for="(p, idx) in iconInfo.paths" :key="idx" :d="p" :fill="iconColor" />
  </svg>
  <span
    v-else-if="!hideFallback"
    class="provider-icon-fallback"
    :style="{ width: size, height: size, fontSize: `calc(${size} * 0.58)`, color: iconColor }"
    aria-hidden="true"
  >
    {{ fallbackText }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import GoogleMark from './GoogleMark.vue'
import { modelIconData } from '@/utils/modelIconData'
import { providerBrandDisplayName, resolveProviderBrand } from '@/utils/providerBrand'

/**
 * ProviderIcon.vue renders provider marks from shared brand artwork or an initial.
 * Google uses the same full-color mark as sign-in unless a caller supplies a
 * color override. Other marks use the brand color, then the graphic color and
 * current text color. Unknown providers with no display name use a question mark,
 * and hideFallback renders nothing in place of the initial or question mark.
 */

/**
 * props supplies the provider name and visual overrides consumed by brand resolution
 * and the selected icon rendering branch.
 */
const props = withDefaults(
  defineProps<{
    brand: string /* provider display brand */
    size?: string /* rendered icon size */
    color?: string /* caller color override */
    // Renders nothing instead of the letter placeholder for brands without artwork, so
    // lists can show logos only where a real mark exists.
    hideFallback?: boolean
  }>(),
  {
    size: '18px',
    color: '',
    hideFallback: false,
  },
)

/**
 * brandInfo resolves the caller brand into the shared display identity, palette, and
 * optional graphic selection.
 */
const brandInfo = computed(() => resolveProviderBrand(props.brand))
/**
 * iconInfo loads the resolved brand graphic or leaves the template to render a text
 * initial.
 */
const iconInfo = computed(() => {
  const iconKey = brandInfo.value.iconKey
  return iconKey ? modelIconData[iconKey] : null
})
/**
 * iconColor selects the fill for single-color marks, masks, and initials. A
 * caller override takes precedence over the brand color, graphic color, and
 * inherited text color.
 */
const iconColor = computed(
  () => props.color || brandInfo.value.iconColor || iconInfo.value?.color || 'currentColor',
)
/**
 * displayName resolves the human readable provider name used to create the initial
 * when no graphic is available.
 */
const displayName = computed(() => providerBrandDisplayName(props.brand))
/**
 * fallbackText uses a question mark for the resolver unknown label and an uppercase
 * first character for other names.
 */
const fallbackText = computed(() =>
  displayName.value === '未知供应商' ? '?' : displayName.value.charAt(0).toUpperCase(),
)
</script>

<style scoped>
.provider-icon,
.provider-icon-fallback {
  flex-shrink: 0;
}

.provider-icon-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  line-height: 1;
}
</style>
