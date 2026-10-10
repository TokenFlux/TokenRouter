import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ProviderTestModal from '../ProviderTestModal.vue'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'

const { getAvailableModels, copyToClipboard } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    providers: {
      getAvailableModels
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'admin.providers.imagePromptDefault': 'Generate a cute orange cat astronaut sticker on a clean pastel background.',
    'admin.providers.textPromptDefault': 'hi'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.providers.imageReceived' && params?.count) {
          return `received-${params.count}`
        }
        return messages[key] || key
      }
    })
  }
})

function createStreamResponse(lines: string[]) {
  const encoder = new TextEncoder()
  const chunks = lines.map((line) => encoder.encode(line))
  let index = 0

  return {
    ok: true,
    body: {
      getReader: () => ({
        read: vi.fn().mockImplementation(async () => {
          if (index < chunks.length) {
            return { done: false, value: chunks[index++] }
          }
          return { done: true, value: undefined }
        })
      })
    }
  } as Response
}

function mountModal(provider: Record<string, unknown> = {
  id: 42,
  name: 'Gemini Image Test',
  platform: 'gemini',
  type: 'apikey',
  status: 'active'
}) {
  return mount(ProviderTestModal, {
    props: {
      show: false,
      provider
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot name="header-actions" /><slot /></div>' },
        Select: { props: ['disabled', 'modelValue'], emits: ['update:modelValue'], template: '<div class="select-stub" :data-disabled="disabled ? \'true\' : \'false\'"></div>' },
        PlatformIcon: true,
        TextArea: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<textarea class="textarea-stub" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        Icon: true
      }
    }
  })
}

// 模拟管理员在型号选择框中选择实际 ID。
async function chooseModel(wrapper: VueWrapper, id: string) {
  wrapper.getComponent('[data-testid="provider-test-model"]').vm.$emit('update:modelValue', id)
  await wrapper.vm.$nextTick()
}

describe('ProviderTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockResolvedValue([
      { id: 'gemini-2.0-flash', display_name: 'Gemini 2.0 Flash' },
      { id: 'gemini-2.5-flash-image', display_name: 'Gemini 2.5 Flash Image' },
      { id: 'gemini-3.1-flash-image', display_name: 'Gemini 3.1 Flash Image' }
    ])
    copyToClipboard.mockReset()
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gemini-2.5-flash-image"}\n',
        'data: {"type":"image","image_url":"data:image/png;base64,QUJD","mime_type":"image/png"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('Jev 发送混合决策问题并展示概率、选择、评分和有效零用量', async () => {
    getAvailableModels.mockResolvedValue([])
    const response = {
      model: 'jev-1.13.0',
      answers: {
        available: { type: 'noul', noul: 0.95 },
        question_2: { type: 'choice', choice: 'a', confidence: 0.8, probabilities: { a: 0.8, b: 0.2 } },
        question_3: { type: 'score', score: 1, confidence: 0.9, probabilities: { 0: 0.1, 1: 0.9 }, legend: 'low to high' }
      },
      usage: { input_tokens: 32, output_tokens: 0 }
    }
    global.fetch = vi.fn().mockResolvedValue(createStreamResponse([
      'data: {"type":"test_start","model":"jev-latest"}\n',
      `data: ${JSON.stringify({ type: 'test_complete', success: true, data: { response, usage_valid: true } })}\n`
    ])) as any
    const wrapper = mountModal({ id: 42, name: 'Jev', platform: 'jev', type: 'apikey' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.find('[data-testid="provider-test-prompt"]').exists()).toBe(false)
    expect(wrapper.getComponent('[data-testid="provider-test-protocol"]').props('disabled')).toBe(true)
    expect(wrapper.text()).not.toContain('admin.providers.testDialog.metricFirstToken')
    await wrapper.get('[data-testid="systemone-state"]').setValue('service ready')
    for (const [index, type] of [[1, 'choice'], [2, 'score']] as const) {
      await wrapper.get('[data-testid="systemone-questions-add"]').trigger('click')
      wrapper.getComponent(`[data-testid="decision-type-${index}"]`).vm.$emit('update:modelValue', type)
      await wrapper.vm.$nextTick()
      await wrapper.get(`[data-testid="decision-instructions-${index}"]`).setValue('Evaluate this state')
    }
    await wrapper.get('[data-testid="provider-test-start"]').trigger('click')
    await flushPromises()
    const body = JSON.parse((global.fetch as any).mock.calls[0][1].body)
    expect(body).toMatchObject({ model_id: 'jev-latest', test_type: 'decision', systemone: { state: 'service ready', questions: { available: { type: 'noul' }, question_2: { type: 'choice', criteria: { a: '', b: '' } }, question_3: { type: 'score', criteria: ['', ''] } } } })
    expect(body).not.toHaveProperty('prompt')
    expect(wrapper.get('[data-testid="decision-answer-available"]').text()).toContain('95.00%')
    expect(wrapper.get('[data-testid="decision-answer-question_2"]').text()).toContain('80.00%')
    expect(wrapper.get('[data-testid="decision-answer-question_3"]').text()).toContain('low to high')
    expect(wrapper.get('[data-testid="systemone-test-result"]').text()).toContain('jev-1.13.0')
    expect(wrapper.text()).not.toContain('admin.providers.decisionTest.usageUnknown')
    const outputPanel = wrapper.get('[data-testid="provider-test-output"]')
    expect(outputPanel.find('[data-testid="decision-answer-available"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.providers.decisionTest.metricUsage')
    expect(wrapper.text()).toContain('32 / 0')
    const jsonView = wrapper.findAll('[role="radio"]').find(button => button.text() === 'JSON' && !button.element.closest('[data-testid="systemone-test-form"]'))!
    await jsonView.trigger('click')
    expect(outputPanel.find('pre').text()).toContain('"answers"')
    await wrapper.get('button[aria-label="admin.providers.testDialog.copy"]').trigger('click')
    expect(copyToClipboard).toHaveBeenLastCalledWith(JSON.stringify(response, null, 2), 'admin.providers.outputCopied')
    await wrapper.findAll('[role="radio"]').find(button => button.text() === 'admin.providers.testDialog.viewLog')!.trigger('click')
    expect(outputPanel.text()).toContain('admin.providers.decisionTest.sending')
    expect(outputPanel.find('[data-testid="decision-answer-available"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('Jev 校验 JSON 状态与问题 ID，结构化状态按原类型发送', async () => {
    const wrapper = mountModal({ id: 42, name: 'Jev', platform: 'jev', type: 'apikey' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const jsonButton = wrapper.findAll('[role="radio"]').find(button => button.text() === 'JSON')!
    await jsonButton.trigger('click')
    await wrapper.get('[data-testid="systemone-state"]').setValue('false')
    expect(wrapper.get('[data-testid="provider-test-start"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="systemone-state"]').setValue('{"ready":true}')
    await wrapper.get('[data-testid="systemone-questions-add"]').trigger('click')
    await wrapper.get('[data-testid="decision-instructions-1"]').setValue('Ready?')
    await wrapper.get('[data-testid="decision-id-1"]').setValue('available')
    expect(wrapper.get('[data-testid="provider-test-start"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="decision-id-1"]').setValue('other')
    await wrapper.get('[data-testid="provider-test-start"]').trigger('click')
    await flushPromises()
    expect(JSON.parse((global.fetch as any).mock.calls[0][1].body).systemone.state).toEqual({ ready: true })
    wrapper.unmount()
  })

  it('Jev 批量测试共用决策问题并使用决策结果详情', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'jev-latest' }, { id: 'jev-preview' }])
    global.fetch = vi.fn().mockImplementation(async (_url, options) => {
      const body = JSON.parse(options.body)
      return createStreamResponse([
        `data: ${JSON.stringify({ type: 'test_complete', success: true, data: { usage_valid: true, response: { model: body.model_id, answers: { available: { type: 'noul', noul: 0.7 } }, usage: { input_tokens: 3, output_tokens: 0 } } } })}\n`
      ])
    }) as any
    const wrapper = mountModal({ id: 42, name: 'Jev', platform: 'jev', type: 'apikey' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    ;(wrapper.vm as any).testScope = 'batch'
    const batch = (wrapper.vm as any).batch
    batch.setMany(['jev-latest', 'jev-preview'], true)
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).not.toContain('admin.providers.testDialog.metricFirstToken')
    await wrapper.get('[data-testid="provider-batch-start"]').trigger('click')
    await flushPromises()
    const bodies = (global.fetch as any).mock.calls.map(([, options]: [unknown, RequestInit]) => JSON.parse(String(options.body)))
    expect(bodies.map((body: any) => body.model_id)).toEqual(['jev-latest', 'jev-preview'])
    expect(bodies[0].test_type).toBe('decision')
    expect(bodies[0].systemone).toEqual(bodies[1].systemone)
    batch.showDetail('jev-preview')
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[data-testid="systemone-test-result"]').text()).toContain('jev-preview')
    expect(wrapper.get('[data-testid="decision-answer-available"]').text()).toContain('70.00%')
    wrapper.unmount()
  })

  it('Jev 用量缺失时仍展示答案及用量提示', async () => {
    global.fetch = vi.fn().mockResolvedValue(createStreamResponse([
      `data: ${JSON.stringify({ type: 'test_complete', success: true, data: { usage_valid: false, response: { model: 'jev-latest', answers: { available: { type: 'noul', noul: 0 } } } } })}\n`
    ])) as any
    const wrapper = mountModal({ id: 42, name: 'Jev', platform: 'jev', type: 'apikey' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-testid="provider-test-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="decision-answer-available"]').text()).toContain('0.00%')
    expect(wrapper.text()).toContain('admin.providers.decisionTest.usageUnknown')
    wrapper.unmount()
  })

  it('空目录提示配置型号，并允许手动输入测试', async () => {
    getAvailableModels.mockResolvedValue([])
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('[data-testid="provider-test-models-empty"]').text()).toContain('admin.providers.testDialog.modelsEmpty')
    expect(wrapper.get('[data-testid="provider-test-model"]').attributes('data-disabled')).toBe('false')
    await chooseModel(wrapper, 'custom-image-model')
    expect(wrapper.getComponent('[data-testid="provider-test-model"]').props('modelValue')).toBe('custom-image-model')
    wrapper.unmount()
  })

  it('gemini 图片模型测试会携带提示词并渲染图片预览', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'gemini-3.1-flash-image')

    ;(wrapper.vm as any).testType = 'image'

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('draw a tiny orange cat astronaut')

    const startButton = wrapper.find('[data-testid="provider-test-start"]')
    expect(startButton.exists()).toBe(true)

    await startButton.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gemini-3.1-flash-image',
      prompt: 'draw a tiny orange cat astronaut',
      test_type: 'image'
    })

    const preview = wrapper.find('img[alt="admin.providers.imagePreviewAlt"]')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('data:image/png;base64,QUJD')
  })

  it('grok 提供商测试使用管理员选择的模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-4.3', display_name: 'Grok 4.3' },
      { id: 'grok-build-0.1', display_name: 'Grok Build 0.1' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"grok-4.3"}\n',
        'data: {"type":"content","text":"ok"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 13,
      name: 'Grok Provider',
      platform: 'grok',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'grok-4.3')

    const startButton = wrapper.find('[data-testid="provider-test-start"]')
    expect(startButton.exists()).toBe(true)

    await startButton.trigger('click')
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-4.3',
      prompt: 'hi',
      test_type: 'text'
    })
  })

  it('OpenAI API Key 显式选择测试协议，保留管理请求标记且不影响提供商配置', async () => {
    const provider = { id: 42, name: 'Protocol test', platform: 'openai', type: 'apikey', status: 'active', extra: { openai_text_route_mode: 'force_responses' } }
    const wrapper = mountModal(provider)
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'gpt-5.4')
    expect(wrapper.find('[data-testid="provider-test-protocol"]').exists()).toBe(true)
    ;(wrapper.vm as any).testProtocol = 'chat_completions'
    await (wrapper.vm as any).startTest()
    const request = (global.fetch as any).mock.calls[0][1]
    expect(JSON.parse(request.body).protocol).toBe('chat_completions')
    expect(request.headers[ADMIN_UI_REQUEST_HEADER]).toBe('1')
    expect(provider.extra.openai_text_route_mode).toBe('force_responses')
    expect(wrapper.find('[data-testid="provider-test-protocol"]').attributes('data-disabled')).toBe('false')
    await wrapper.setProps({ provider: { ...provider, type: 'oauth' } } as any)
    // OAuth 只有 Codex Responses，协议框保留展示但不可选。
    expect(wrapper.find('[data-testid="provider-test-protocol"]').attributes('data-disabled')).toBe('true')
    expect((wrapper.vm as any).testProtocol).toBe('responses')
    wrapper.unmount()
  })

  it('OpenAI 原生 V2 压缩探测会携带 compact 测试模式', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'compact'
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="provider-test-prompt"]').exists()).toBe(false)
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({
      model_id: 'gpt-5.4',
      prompt: '',
      test_type: 'text',
      mode: 'compact'
    })
  })

  it('OpenAI 旧版 Compact 兼容性测试会携带 legacy_compact 模式', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 43,
      name: 'OpenAI legacy compact',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'legacy_compact'
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="provider-test-prompt"]').exists()).toBe(false)
    await (wrapper.vm as any).startTest()
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({ mode: 'legacy_compact', prompt: '', test_type: 'text' })
  })

  it('文字测试会发送自定义提示词和显式类型', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gemini-custom-text', display_name: 'Custom text model' }])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse(['data: {"type":"test_complete","success":true}\n'])
    ) as any

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'gemini-custom-text')
    await wrapper.find('textarea.textarea-stub').setValue('say hello in one sentence')

    await wrapper.find('[data-testid="provider-test-start"]').trigger('click')
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({
      model_id: 'gemini-custom-text',
      prompt: 'say hello in one sentence',
      test_type: 'text'
    })
  })

  it('国产平台可只测一个已启用协议，也可依次测试全部协议', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'glm-4.7', display_name: 'GLM 4.7' }])
    global.fetch = vi.fn().mockImplementation(async () =>
      createStreamResponse(['data: {"type":"test_complete","success":true}\n'])
    ) as any

    const wrapper = mountModal({
      id: 7,
      name: 'Zhipu',
      platform: 'zhipu',
      type: 'apikey',
      status: 'active',
      credentials: { upstream_protocols: ['anthropic_messages', 'openai_chat_completions'] }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'glm-4.7')

    expect((wrapper.vm as any).testProtocol).toBe('chat_completions')
    expect((wrapper.vm as any).protocolOptions.map((item: { value: string }) => item.value)).toEqual(['chat_completions', 'anthropic', 'all'])

    ;(wrapper.vm as any).testProtocol = 'anthropic'
    await (wrapper.vm as any).startTest()
    await flushPromises()
    expect(JSON.parse((global.fetch as any).mock.calls[0][1].body).protocol).toBe('anthropic')

    ;(wrapper.vm as any).testProtocol = 'all'
    await (wrapper.vm as any).startTest()
    await flushPromises()
    expect(JSON.parse((global.fetch as any).mock.calls[1][1].body)).not.toHaveProperty('protocol')
  })

  it('只启用一个协议的国产平台不发送协议字段', async () => {
    const wrapper = mountModal({
      id: 8,
      name: 'Kimi',
      platform: 'kimi',
      type: 'apikey',
      status: 'active',
      credentials: { upstream_protocols: ['openai_responses'] }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'kimi-k2.5')

    expect(wrapper.find('[data-testid="provider-test-protocol"]').attributes('data-disabled')).toBe('true')
    await (wrapper.vm as any).startTest()
    await flushPromises()
    expect(JSON.parse((global.fetch as any).mock.calls[0][1].body)).not.toHaveProperty('protocol')
  })

  it('展示回复、实际模型和过程日志', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-5.4', display_name: 'GPT-5.4' }])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gpt-5.4-mapped"}\n',
        'data: {"type":"status","text":"正在通过 /v1/chat/completions 测试连接"}\n',
        'data: {"type":"content","text":"hello"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({ id: 9, name: 'OpenAI', platform: 'openai', type: 'apikey', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'gpt-5.4')
    await wrapper.find('[data-testid="provider-test-start"]').trigger('click')
    await flushPromises()

    expect((wrapper.vm as any).singleRun.status).toBe('success')
    expect(wrapper.find('[data-testid="provider-test-output"]').text()).toContain('hello')
    expect(wrapper.text()).toContain('gpt-5.4-mapped')
    expect((wrapper.vm as any).singleRun.firstTokenMs).not.toBeNull()
    expect((wrapper.vm as any).singleRun.totalMs).not.toBeNull()

    const logButton = wrapper.findAll('button').find((button) => button.text() === 'admin.providers.testDialog.viewLog')
    await logButton!.trigger('click')
    expect(wrapper.find('[data-testid="provider-test-output"]').text()).toContain('/v1/chat/completions')
  })

  it('错误事件后补发的完成事件不会覆盖失败结果', async () => {
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"error","error":"upstream 401"}\n',
        'data: {"type":"test_complete","success":false,"error":"later"}\n'
      ])
    ) as any

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await chooseModel(wrapper, 'gemini-2.0-flash')
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect((wrapper.vm as any).singleRun.status).toBe('error')
    expect((wrapper.vm as any).singleRun.errorMessage).toBe('upstream 401')
  })

  it('批量模型测试按所选模型逐个请求，并可重试失败的模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'model-a', display_name: 'A' },
      { id: 'model-b', display_name: 'B' },
      { id: 'model-c', display_name: 'C' }
    ])
    global.fetch = vi.fn().mockImplementation(async (_url: string, request: { body: string }) => {
      const model = JSON.parse(request.body).model_id
      return createStreamResponse(
        model === 'model-b'
          ? ['data: {"type":"error","error":"boom"}\n']
          : [`data: {"type":"test_start","model":"${model}"}\n`, 'data: {"type":"content","text":"ok"}\n', 'data: {"type":"test_complete","success":true}\n']
      )
    }) as any

    const wrapper = mountModal({ id: 21, name: 'OpenAI', platform: 'openai', type: 'apikey', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).testScope = 'batch'
    await wrapper.vm.$nextTick()
    const batch = (wrapper.vm as any).batch
    expect([...batch.selected]).toEqual([])
    batch.toggle('model-a', true)
    batch.toggle('model-b', true)
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-testid="provider-batch-start"]').trigger('click')
    await flushPromises()

    const requested = (global.fetch as any).mock.calls.map((call: [string, { body: string }]) => JSON.parse(call[1].body))
    expect(requested.map((body: { model_id: string }) => body.model_id).sort()).toEqual(['model-a', 'model-b'])
    expect(requested.every((body: { test_type: string; prompt: string }) => body.test_type === 'text' && body.prompt === 'hi')).toBe(true)
    expect(batch.rows['model-a'].state).toBe('success')
    expect(batch.rows['model-b'].state).toBe('failed')
    expect(batch.rows['model-c']).toBeUndefined()

    await wrapper.find('[data-testid="provider-batch-retry"]').trigger('click')
    await flushPromises()
    expect((global.fetch as any).mock.calls).toHaveLength(3)
    expect(JSON.parse((global.fetch as any).mock.calls[2][1].body).model_id).toBe('model-b')
    expect(batch.rows['model-a'].state).toBe('success')
  })

  it('停止批量测试后未开始的模型标记为未执行', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'model-a', display_name: 'A' },
      { id: 'model-b', display_name: 'B' },
      { id: 'model-c', display_name: 'C' }
    ])
    let release: () => void = () => {}
    global.fetch = vi.fn().mockImplementation(async () => {
      await new Promise<void>((resolve) => { release = resolve })
      return createStreamResponse(['data: {"type":"test_complete","success":true}\n'])
    }) as any

    const wrapper = mountModal({ id: 22, name: 'OpenAI', platform: 'openai', type: 'apikey', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    ;(wrapper.vm as any).testScope = 'batch'
    const batch = (wrapper.vm as any).batch
    batch.setMany(['model-a', 'model-b', 'model-c'], true)
    batch.concurrency = 1
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-testid="provider-batch-start"]').trigger('click')
    await flushPromises()
    expect(batch.running).toBe(true)
    await wrapper.find('[data-testid="provider-batch-stop"]').trigger('click')
    release()
    await flushPromises()

    expect((global.fetch as any).mock.calls).toHaveLength(1)
    expect(batch.rows['model-a'].state).toBe('success')
    expect(batch.rows['model-b'].state).toBe('stopped')
    expect(batch.rows['model-c'].state).toBe('stopped')
    expect(batch.running).toBe(false)
  })

  it.each(['anthropic', 'openai', 'gemini', 'grok', 'kimi', 'zhipu', 'deepseek', 'qoder', 'antigravity'])('%s 测试打开后等待管理员选择，不默认选中目录中的 Sonnet', async platform => {
    getAvailableModels.mockResolvedValue([
      { id: '302ai/claude-sonnet-4-5-20250929', display_name: 'Claude Sonnet' },
      { id: 'custom-model', display_name: 'Custom Model' }
    ])
    const wrapper = mountModal({ id: 91, name: 'Provider', platform, type: 'apikey', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.getComponent('[data-testid="provider-test-model"]').props('modelValue')).toBe('')
    expect(wrapper.get('[data-testid="provider-test-start"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="provider-test-start"]').trigger('click')
    expect(global.fetch).not.toHaveBeenCalled()
    expect([...(wrapper.vm as any).batch.selected]).toEqual([])
    await chooseModel(wrapper, 'custom-model')
    expect(wrapper.getComponent('[data-testid="provider-test-model"]').props('modelValue')).toBe('custom-model')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.getComponent('[data-testid="provider-test-model"]').props('modelValue')).toBe('')
    wrapper.unmount()
  })

})
