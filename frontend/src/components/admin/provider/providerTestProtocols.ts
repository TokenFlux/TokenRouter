import type { Provider, ProtocolID } from '@/types'

/** 连接测试可选择的协议，取值与后端 TestRequest.Protocol 一致。 */
export type ProviderTestProtocol = 'responses' | 'chat_completions' | 'anthropic' | 'systemone'

export interface ProviderTestProtocolOption {
  /** 固定端点使用 native 选项，提交请求时省略 protocol。 */
  value: ProviderTestProtocol | 'native'
  label: string
  path: string
}

export interface ProviderTestProtocolPlan {
  options: ProviderTestProtocolOption[]
  /** 只有一个端点时禁用选择，请求省略 protocol。 */
  selectable: boolean
}

const CN_PLATFORMS = new Set(['kimi', 'zhipu', 'deepseek'])

// 国产平台原生协议与测试协议的对应关系，顺序即下拉框顺序。
const CN_PROTOCOLS: Array<{ id: ProtocolID; value: ProviderTestProtocol }> = [
  { id: 'openai_chat_completions', value: 'chat_completions' },
  { id: 'anthropic_messages', value: 'anthropic' },
  { id: 'openai_responses', value: 'responses' }
]

function option(value: ProviderTestProtocolOption['value'], label: string, path: string): ProviderTestProtocolOption {
  return { value, label, path }
}

function chatOption() {
  return option('chat_completions', 'Chat Completions', '/v1/chat/completions')
}

function messagesOption() {
  return option('anthropic', 'Messages', '/v1/messages')
}

function responsesOption(platform: string) {
  // DeepSeek 原生 Responses 端点不带 /v1 前缀。
  return option('responses', 'Responses', platform === 'deepseek' ? '/responses' : '/v1/responses')
}

function cnOption(value: ProviderTestProtocol, platform: string) {
  if (value === 'chat_completions') return chatOption()
  if (value === 'anthropic') return messagesOption()
  return responsesOption(platform)
}

// enabledProtocols 读取提供商保存的原生协议集合，缺少字段的旧记录返回 null。
function enabledProtocols(provider: Provider): ProtocolID[] | null {
  const raw = provider.credentials?.upstream_protocols
  return Array.isArray(raw) ? (raw as ProtocolID[]) : null
}

// legacyCNProtocols 按旧版 api_protocol 推导国产平台可测试的协议，与后端兼容规则保持一致。
function legacyCNProtocols(provider: Provider): ProviderTestProtocol[] {
  const legacy = provider.credentials?.api_protocol
  const nativeResponses = provider.platform !== 'zhipu'
  if (legacy === 'adaptive') {
    return nativeResponses ? ['chat_completions', 'anthropic', 'responses'] : ['chat_completions', 'anthropic']
  }
  if (legacy === 'anthropic') return ['anthropic']
  if (legacy === 'responses' && nativeResponses) return ['responses']
  return ['chat_completions']
}

/** 根据平台、认证方式和已启用协议列出本次连接测试可用的端点。 */
export function providerTestProtocolPlan(provider: Provider | null): ProviderTestProtocolPlan {
  if (!provider) return { options: [], selectable: false }
  const { platform, type } = provider

  if (platform === 'openai') {
    // API Key 可直连两种文字端点，与提供商是否启用无关；OAuth 只有 Codex Responses。
    if (type === 'apikey') {
      return { options: [responsesOption(platform), chatOption()], selectable: true }
    }
    return { options: [responsesOption(platform)], selectable: false }
  }

  if (CN_PLATFORMS.has(platform)) {
    const enabled = enabledProtocols(provider)
    const values = enabled
      ? CN_PROTOCOLS.filter((item) => enabled.includes(item.id)).map((item) => item.value)
      : legacyCNProtocols(provider)
    return { options: values.map((value) => cnOption(value, platform)), selectable: values.length > 1 }
  }

  // 其他平台只有一个测试端点，仅用于展示。
  switch (platform) {
    case 'jev':
      return { options: [option('native', 'SystemOne', '/v1/systemone')], selectable: false }
    case 'anthropic':
      return { options: [option('native', 'Messages', '/v1/messages')], selectable: false }
    case 'grok':
      return { options: [option('native', 'Responses', '/v1/responses')], selectable: false }
    case 'qoder':
      return { options: [option('native', 'Chat Completions', '/v1/chat/completions')], selectable: false }
    case 'antigravity':
      return { options: [option('native', 'GenerateContent', ':streamGenerateContent')], selectable: false }
    case 'gemini':
      return { options: [option('native', 'GenerateContent', ':streamGenerateContent')], selectable: false }
    default:
      return { options: [], selectable: false }
  }
}

/** 打开弹窗时的默认协议：OpenAI API Key 未启用 Responses 时优先 Chat，其余取第一个端点。 */
export function defaultProviderTestProtocol(provider: Provider | null, plan: ProviderTestProtocolPlan) {
  const first = plan.options[0]?.value ?? 'native'
  if (provider?.platform === 'openai' && provider.type === 'apikey') {
    const enabled = enabledProtocols(provider)
    if (enabled && !enabled.includes('openai_responses') && enabled.includes('openai_chat_completions')) {
      return 'chat_completions'
    }
  }
  return first
}
