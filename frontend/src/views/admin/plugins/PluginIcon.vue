<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'

// PluginIcon.vue renders a publisher icon inside the plugin tile used by lists, cards,
// headers, and dialogs. Package and catalog icons are optional and may fail to load,
// so the tile falls back to the native puzzle symbol and keeps the same footprint.
// The largest size uses the surface radius; smaller tiles use the control radius.

/** PluginIconSize lists the supported tile edges in pixels. */
export type PluginIconSize = 32 | 40 | 48 | 64

// Props carries the resolved icon location; an empty string selects the fallback.
const props = withDefaults(
  defineProps<{
    src: string // Resolved image URL or empty string.
    size?: PluginIconSize // Tile edge in pixels.
  }>(),
  { size: 40 },
)

// failed remembers a broken image for the current source so rerenders do not retry it.
const failed = ref(false)

// SIZE_CLASSES map each tile edge onto fixed dimensions, radius, and inner padding.
const SIZE_CLASSES: Record<PluginIconSize, string> = {
  32: 'h-8 w-8 rounded-control p-1',
  40: 'h-10 w-10 rounded-control p-1.5',
  48: 'h-12 w-12 rounded-control p-1.5',
  64: 'h-16 w-16 rounded-surface p-2',
}

// FALLBACK_SIZES keep the puzzle symbol proportional to the tile.
const FALLBACK_SIZES = { 32: 'sm', 40: 'md', 48: 'lg', 64: 'xl' } as const

// tileClass selects the classes for the requested tile size.
const tileClass = computed(() => SIZE_CLASSES[props.size])

// A new source, such as the icon of an updated version, deserves a fresh load attempt
// even when the previous image failed.
watch(
  () => props.src,
  () => {
    failed.value = false
  },
)
</script>

<template>
  <span
    class="flex shrink-0 items-center justify-center overflow-hidden border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-950"
    :class="tileClass"
    aria-hidden="true"
  >
    <img
      v-if="src && !failed"
      :src="src"
      alt=""
      class="h-full w-full object-contain"
      referrerpolicy="no-referrer"
      @error="failed = true"
    />
    <Icon
      v-else
      name="puzzle"
      :size="FALLBACK_SIZES[size]"
      class="text-gray-400 dark:text-dark-400"
      :animate-on-hover="false"
    />
  </span>
</template>
