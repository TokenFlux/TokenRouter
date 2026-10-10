import type { ProtocolID } from '@/types'
import type { ProviderBrandKey } from '@/utils/providerBrand'

// MarketplaceProtocol 是模型广场展示的一个客户端协议：品牌图标、端点和 i18n 短名的键。
export interface MarketplaceProtocol {
  id: ProtocolID
  // brand 传给 ProviderIcon，表示请求格式所属的厂商。
  brand: Extract<ProviderBrandKey, 'anthropic' | 'openai' | 'google' | 'xai' | 'typesafe'>
  endpoint: string
}

// 模型广场对未登录用户开放，读不到管理员的协议目录，所以端点在这里写一份。
// 顺序和端点要与后端协议目录一致，单元测试用前端的目录夹具核对。
export const MARKETPLACE_PROTOCOLS: readonly MarketplaceProtocol[] = [
  { id: 'anthropic_messages', brand: 'anthropic', endpoint: 'POST /v1/messages' },
  { id: 'openai_responses', brand: 'openai', endpoint: 'POST /v1/responses' },
  { id: 'openai_chat_completions', brand: 'openai', endpoint: 'POST /v1/chat/completions' },
  { id: 'gemini_generate_content', brand: 'google', endpoint: 'POST /v1beta/models/{model}:generateContent / :streamGenerateContent' },
  { id: 'systemone', brand: 'typesafe', endpoint: 'POST /v1/systemone' },
  { id: 'openai_embeddings', brand: 'openai', endpoint: 'POST /v1/embeddings' },
  { id: 'openai_images_generations', brand: 'openai', endpoint: 'POST /v1/images/generations' },
  { id: 'openai_images_edits', brand: 'openai', endpoint: 'POST /v1/images/edits' },
  { id: 'image_batches', brand: 'google', endpoint: 'POST /v1/images/batches' },
  { id: 'grok_videos_generations', brand: 'xai', endpoint: 'POST /v1/videos/generations' },
  { id: 'grok_videos_edits', brand: 'xai', endpoint: 'POST /v1/videos/edits' },
  { id: 'grok_videos_extensions', brand: 'xai', endpoint: 'POST /v1/videos/extensions' },
  { id: 'grok_tts', brand: 'xai', endpoint: 'POST /v1/tts' },
  { id: 'grok_stt', brand: 'xai', endpoint: 'POST /v1/stt' },
  { id: 'grok_custom_voices', brand: 'xai', endpoint: '/v1/custom-voices' },
  { id: 'grok_voice_realtime', brand: 'xai', endpoint: 'GET /v1/realtime (WebSocket)' },
  { id: 'openai_responses_websocket', brand: 'openai', endpoint: 'GET /v1/responses (WebSocket)' },
  { id: 'openai_live', brand: 'openai', endpoint: 'POST /v1/live' },
  { id: 'openai_responses_compact', brand: 'openai', endpoint: 'POST /v1/responses/compact' },
  { id: 'openai_alpha_search', brand: 'openai', endpoint: 'POST /v1/alpha/search' },
  { id: 'grok_web_search', brand: 'xai', endpoint: 'POST /v1/web_search' },
  { id: 'grok_x_search', brand: 'xai', endpoint: 'POST /v1/x_search' },
]

// marketplaceProtocols 按目录顺序返回接口下发的协议，目录里没有的 ID 跳过。
export function marketplaceProtocols(ids: readonly ProtocolID[] | undefined): MarketplaceProtocol[] {
  const selected = new Set(ids ?? [])
  return MARKETPLACE_PROTOCOLS.filter(protocol => selected.has(protocol.id))
}
