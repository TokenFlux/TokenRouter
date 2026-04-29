<template>
  <svg
    v-if="iconInfo"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    xmlns="http://www.w3.org/2000/svg"
    class="model-icon"
    fill="currentColor"
    fill-rule="evenodd"
  >
    <path v-for="(p, idx) in iconInfo.paths" :key="idx" :d="p" :fill="iconInfo.color" />
  </svg>
  <span v-else class="model-icon-fallback" :style="{ width: size, height: size, fontSize: `calc(${size} * 0.5)` }">
    {{ fallbackText }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { modelIconData } from '@/utils/modelIconData'

const props = withDefaults(defineProps<{
  model: string
  size?: string
}>(), {
  size: '18px'
})

const fallbackText = computed(() => props.model.charAt(0).toUpperCase())

const iconKey = computed(() => {
  const modelLower = props.model.toLowerCase()

  // OpenAI 模型
  if (modelLower.startsWith('gpt') || modelLower.startsWith('o1') ||
      modelLower.startsWith('o3') || modelLower.startsWith('o4') ||
      modelLower.includes('chatgpt') || modelLower.includes('dall-e') ||
      modelLower.includes('whisper') || modelLower.includes('tts-1') ||
      modelLower.includes('text-embedding-3') || modelLower.includes('text-moderation') ||
      modelLower.includes('babbage') || modelLower.includes('davinci') ||
      modelLower.includes('curie') || modelLower.includes('ada')) return 'openai'

  // Anthropic Claude 模型
  if (modelLower.includes('claude')) return 'claude'

  // Google Gemini 模型
  if (modelLower.includes('gemini') || modelLower.includes('gemma') ||
      modelLower.includes('learnlm') || modelLower.includes('imagen-') ||
      modelLower.includes('veo-')) return 'gemini'

  // 智谱 GLM 模型
  if (modelLower.includes('glm') || modelLower.includes('chatglm') ||
      modelLower.includes('cogview') || modelLower.includes('cogvideo')) return 'zhipu'

  // 阿里 Qwen 模型
  if (modelLower.includes('qwen') || modelLower.includes('qwq')) return 'qwen'

  // DeepSeek 模型
  if (modelLower.includes('deepseek')) return 'deepseek'

  // Mistral 模型
  if (modelLower.includes('mistral') || modelLower.includes('mixtral') ||
      modelLower.includes('codestral') || modelLower.includes('pixtral') ||
      modelLower.includes('voxtral') || modelLower.includes('magistral')) return 'mistral'

  // Meta Llama 模型
  if (modelLower.includes('llama')) return 'meta'

  // Cohere 模型
  if (modelLower.includes('command') || modelLower.includes('c4ai-') ||
      modelLower.includes('embed-')) return 'cohere'

  // 零一万物 Yi 模型
  if (modelLower.startsWith('yi-') || modelLower.startsWith('yi ')) return 'yi'

  // xAI Grok 模型
  if (modelLower.includes('grok')) return 'xai'

  // Moonshot 模型
  if (modelLower.includes('moonshot') || modelLower.includes('kimi')) return 'moonshot'

  // 字节豆包模型
  if (modelLower.includes('doubao')) return 'doubao'

  // MiniMax 模型
  if (modelLower.includes('abab') || modelLower.includes('minimax')) return 'minimax'

  // 百度文心模型
  if (modelLower.includes('ernie') || modelLower.includes('wenxin')) return 'wenxin'

  // 讯飞星火模型
  if (modelLower.includes('spark')) return 'spark'

  // 腾讯混元模型
  if (modelLower.includes('hunyuan')) return 'hunyuan'

  // Cloudflare 模型
  if (modelLower.includes('@cf/')) return 'cloudflare'

  // Midjourney 模型
  if (modelLower.includes('mj_') || modelLower.includes('midjourney')) return 'midjourney'

  // Perplexity 模型
  if (modelLower.includes('perplexity') || modelLower.includes('pplx')) return 'perplexity'

  // Jina 模型
  if (modelLower.includes('jina')) return 'jina'

  // OpenRouter 模型
  if (modelLower.includes('openrouter')) return 'openrouter'

  // Suno 模型
  if (modelLower.includes('suno')) return 'suno'

  // Ollama 模型
  if (modelLower.includes('ollama')) return 'ollama'

  // 360 模型
  if (modelLower.includes('360')) return 'ai360'

  // Dify 模型
  if (modelLower.includes('dify')) return 'dify'

  // Coze 模型
  if (modelLower.includes('coze')) return 'coze'

  return null
})

const iconInfo = computed(() => iconKey.value ? modelIconData[iconKey.value] : null)
</script>

<style scoped>
.model-icon {
  flex-shrink: 0;
}
.model-icon-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
  color: white;
  font-weight: 600;
  flex-shrink: 0;
}
</style>
