<template>
  <span
    v-if="iconInfo?.maskUrl"
    class="inline-block shrink-0"
    :style="{
      width: size,
      height: size,
      backgroundColor: iconInfo.color,
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
    class="model-icon"
    :class="iconKey === 'openai' ? 'text-black dark:text-white' : undefined"
    fill="currentColor"
    fill-rule="evenodd"
  >
    <path v-for="(p, idx) in iconInfo.paths" :key="idx" :d="p" :fill="iconInfo.color" />
  </svg>
  <span
    v-else-if="!hideFallback"
    class="model-icon-fallback"
    :style="{ width: size, height: size, fontSize: `calc(${size} * 0.5)` }"
  >
    {{ fallbackText }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { resolveProviderBrandKey } from '@/utils/providerBrand'
import { modelIconData } from '@/utils/modelIconData'

/**
 * ModelIcon.vue selects a model brand graphic from its name and renders the matching
 * icon data. Ordered name checks resolve overlapping matches, including the Typesafe
 * brand override, while unrecognized model names use their uppercase first character
 * as a compact placeholder unless hideFallback asks for no mark at all.
 */

/**
 * props supplies the model name used for brand matching and a CSS size shared by the
 * graphic and initial placeholder.
 */
const props = withDefaults(
  defineProps<{
    model: string /* model name identifier */
    size?: string /* rendered icon size */
    // Renders nothing instead of the initial placeholder for unrecognized models, so
    // lists can show logos only where a real mark exists.
    hideFallback?: boolean
  }>(),
  {
    size: '18px',
    hideFallback: false,
  },
)

/**
 * fallbackText supplies an uppercase model initial for names that do not resolve to a
 * known brand graphic.
 */
const fallbackText = computed(() => props.model.charAt(0).toUpperCase())

/**
 * iconKey checks model name patterns in precedence order after the Typesafe override,
 * returning null when no listed brand matches.
 */
const iconKey = computed(() => {
  const modelLower = props.model.toLowerCase()
  /**
   * The Typesafe brand resolver takes precedence over broad model substrings, allowing
   * branded model names to retain their configured graphic.
   */
  if (resolveProviderBrandKey(props.model) === 'typesafe') return 'typesafe'

  if (
    modelLower.startsWith('gpt') ||
    modelLower.startsWith('codex') ||
    modelLower.startsWith('o1') ||
    modelLower.startsWith('o3') ||
    modelLower.startsWith('o4') ||
    modelLower.includes('chatgpt') ||
    modelLower.includes('dall-e') ||
    modelLower.includes('whisper') ||
    modelLower.includes('tts-1') ||
    modelLower.includes('text-embedding-3') ||
    modelLower.includes('text-moderation') ||
    modelLower.includes('babbage') ||
    modelLower.includes('davinci') ||
    modelLower.includes('curie') ||
    modelLower.includes('ada')
  )
    return 'openai'

  if (modelLower.includes('claude')) return 'claude'

  if (
    modelLower.includes('gemini') ||
    modelLower.includes('gemma') ||
    modelLower.includes('learnlm') ||
    modelLower.includes('imagen-') ||
    modelLower.includes('veo-') ||
    modelLower.includes('nano-banana')
  )
    return 'gemini'

  if (
    modelLower.includes('glm') ||
    modelLower.includes('chatglm') ||
    modelLower.includes('cogview') ||
    modelLower.includes('cogvideo')
  )
    return 'zhipu'

  if (modelLower.includes('qwen') || modelLower.includes('qwq')) return 'qwen'

  if (modelLower.includes('deepseek')) return 'deepseek'

  if (
    modelLower.includes('mistral') ||
    modelLower.includes('mixtral') ||
    modelLower.includes('codestral') ||
    modelLower.includes('pixtral') ||
    modelLower.includes('voxtral') ||
    modelLower.includes('magistral')
  )
    return 'mistral'

  if (modelLower.includes('llama')) return 'meta'

  if (
    modelLower.includes('command') ||
    modelLower.includes('c4ai-') ||
    modelLower.includes('embed-')
  )
    return 'cohere'

  if (modelLower.startsWith('yi-') || modelLower.startsWith('yi ')) return 'yi'

  if (modelLower.includes('grok')) return 'xai'

  if (modelLower.includes('moonshot') || modelLower.includes('kimi')) return 'moonshot'

  if (modelLower.includes('doubao')) return 'doubao'

  if (modelLower.includes('abab') || modelLower.includes('minimax')) return 'minimax'

  if (modelLower.includes('ernie') || modelLower.includes('wenxin')) return 'wenxin'

  if (modelLower.includes('spark')) return 'spark'

  if (modelLower.includes('hunyuan')) return 'hunyuan'

  if (modelLower.includes('@cf/')) return 'cloudflare'

  if (modelLower.includes('mj_') || modelLower.includes('midjourney')) return 'midjourney'

  if (modelLower.includes('perplexity') || modelLower.includes('pplx')) return 'perplexity'

  if (modelLower.includes('jina')) return 'jina'

  if (modelLower.includes('openrouter')) return 'openrouter'

  if (modelLower.includes('suno')) return 'suno'

  if (modelLower.includes('ollama')) return 'ollama'

  if (modelLower.includes('360')) return 'ai360'

  if (modelLower.includes('dify')) return 'dify'

  if (modelLower.includes('coze')) return 'coze'

  /**
   * When no model name pattern matches, leave graphic selection empty so the template
   * can identify the model by its initial.
   */
  return null
})

/**
 * iconInfo resolves the selected brand graphic or leaves the template on its initial
 * placeholder path.
 */
const iconInfo = computed(() => (iconKey.value ? modelIconData[iconKey.value] : null))
</script>

<style scoped>
/*
 * Brand graphics and generated initials are decorative marks; excluding them
 * from text selection keeps adjacent model labels straightforward to copy.
 */
.model-icon,
.model-icon-fallback {
  @apply select-none;
}

.model-icon {
  flex-shrink: 0;
}
.model-icon-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-compact);
  @apply bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-300;
  font-weight: 600;
  flex-shrink: 0;
}
</style>
