import { reactive } from 'vue'
import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'
import type { SystemOneTestPayload, SystemOneTestResult } from './systemOneTest'
import type { ProviderTestProtocol } from './providerTestProtocols'

export type ProviderTestStatus = 'idle' | 'connecting' | 'success' | 'error'
export type ProviderTestLogTone = 'info' | 'muted' | 'success' | 'error'

export interface ProviderTestLogLine {
  text: string
  tone: ProviderTestLogTone
}

export interface ProviderTestImage {
  url: string
  mimeType?: string
}

/** 一次连接测试的全部可展示状态，单模型和批量详情共用。 */
export interface ProviderTestRun {
  status: ProviderTestStatus
  logLines: ProviderTestLogLine[]
  replyText: string
  errorMessage: string
  resolvedModel: string
  firstTokenMs: number | null
  totalMs: number | null
  images: ProviderTestImage[]
  decisionResult?: SystemOneTestResult | null
  decisionUsageValid?: boolean
}

/** 发给 POST /admin/providers/:id/test 的请求体。 */
export interface ProviderTestRequestBody {
  model_id: string
  prompt?: string
  systemone?: SystemOneTestPayload
  test_type: 'text' | 'image' | 'decision'
  mode?: 'default' | 'compact' | 'legacy_compact'
  protocol?: ProviderTestProtocol
}

interface ProviderTestEvent {
  type: string
  text?: string
  model?: string
  success?: boolean
  error?: string
  image_url?: string
  mime_type?: string
  data?: { response?: SystemOneTestResult; usage_valid?: boolean; duration_ms?: number }
}

type Translate = (key: string, params?: Record<string, unknown>) => string

export function createProviderTestRun(): ProviderTestRun {
  return reactive({
    status: 'idle',
    logLines: [],
    replyText: '',
    errorMessage: '',
    resolvedModel: '',
    firstTokenMs: null,
    totalMs: null,
    images: []
  })
}

export function resetProviderTestRun(run: ProviderTestRun) {
  run.status = 'idle'
  run.logLines = []
  run.replyText = ''
  run.errorMessage = ''
  run.resolvedModel = ''
  run.firstTokenMs = null
  run.totalMs = null
  run.images = []
  run.decisionResult = null
  run.decisionUsageValid = false
}

/**
 * 执行一次 SSE 连接测试并把事件写入 run。
 * 主动中止时返回 false 且状态回到 idle，其余情况都以 success 或 error 结束。
 */
export async function executeProviderTest(options: {
  providerId: number
  providerName: string
  body: ProviderTestRequestBody
  run: ProviderTestRun
  signal: AbortSignal
  t: Translate
}): Promise<boolean> {
  const { run, t } = options
  resetProviderTestRun(run)
  run.status = 'connecting'
  const startedAt = performance.now()
  const elapsed = () => Math.round(performance.now() - startedAt)

  const addLine = (text: string, tone: ProviderTestLogTone = 'muted') => {
    run.logLines.push({ text, tone })
  }
  // 首段文字或首张图片到达的时间记为首字延迟。
  const markFirstToken = () => {
    if (run.firstTokenMs == null) run.firstTokenMs = elapsed()
  }
  // 第一次结束事件确定测试结果，后端可能在错误后补发完成事件。
  const finish = (status: 'success' | 'error', message = '') => {
    if (run.status !== 'connecting') return
    run.status = status
    run.totalMs = elapsed()
    if (status === 'error') {
      run.errorMessage = message
      addLine(t('admin.providers.errorPrefix', { message }), 'error')
    } else {
      addLine(t('admin.providers.testCompleted'), 'success')
    }
  }

  const handleEvent = (event: ProviderTestEvent) => {
    switch (event.type) {
      case 'test_start':
        addLine(t('admin.providers.connectedToApi'), 'success')
        if (event.model) {
          run.resolvedModel = event.model
          addLine(t('admin.providers.usingModel', { model: event.model }))
        }
        addLine(
          options.body.test_type === 'image'
            ? t('admin.providers.sendingImageRequest')
            : options.body.test_type === 'decision'
              ? t('admin.providers.decisionTest.sending')
              : t('admin.providers.sendingTestMessage')
        )
        break
      case 'content':
        if (event.text) {
          if (options.body.test_type !== 'decision') markFirstToken()
          run.replyText += event.text
        }
        break
      case 'image':
        if (event.image_url) {
          markFirstToken()
          run.images.push({ url: event.image_url, mimeType: event.mime_type })
          addLine(t('admin.providers.imageReceived', { count: run.images.length }), 'info')
        }
        break
      case 'status':
        if (event.text) addLine(event.text, 'info')
        break
      case 'test_complete':
        if (event.success) {
          if (options.body.test_type === 'decision' && run.status === 'connecting') {
            run.decisionResult = event.data?.response ?? null
            run.decisionUsageValid = event.data?.usage_valid === true
          }
          finish('success')
        } else {
          finish('error', event.error || 'Test failed')
        }
        break
      case 'error':
        finish('error', event.error || 'Unknown error')
        break
    }
  }

  addLine(t('admin.providers.startingTestForProvider', { name: options.providerName }), 'info')

  try {
    // SSE 测试接口通过 fetch 发送 POST 请求，地址使用配置的 API base。
    const response = await fetch(buildApiUrl(`/admin/providers/${options.providerId}/test`), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
        'Content-Type': 'application/json',
        [ADMIN_UI_REQUEST_HEADER]: '1'
      },
      body: JSON.stringify(options.body),
      signal: options.signal
    })
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }
    const reader = response.body?.getReader()
    if (!reader) {
      throw new Error('No response body')
    }

    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (!line.startsWith('data: ')) continue
        const jsonStr = line.slice(6).trim()
        if (!jsonStr) continue
        try {
          handleEvent(JSON.parse(jsonStr))
        } catch (e) {
          console.error('Failed to parse SSE event:', e)
        }
      }
    }

    // 连接在完成事件前结束时将测试状态设为失败。
    finish('error', t('admin.providers.testDialog.streamEnded'))
    return true
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      run.status = 'idle'
      return false
    }
    finish('error', error instanceof Error ? error.message : 'Unknown error')
    return true
  }
}
