import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ModelAttributesView from '../ModelAttributesView.vue'
import { modelAttributesAPI } from '@/api/admin/modelAttributes'
import ModelAttributesFields from '@/components/admin/model-attributes/ModelAttributesFields.vue'
import ModelAttributeRouteFields from '@/components/admin/model-attributes/ModelAttributeRouteFields.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import LocalizedEditor from '@/components/common/LocalizedEditor.vue'
import type { ModelAttributes } from '@/api/modelAttributes.types'
import { originalContent } from '@/i18n/content'

// ModelAttributesView.spec.ts checks attribute draft persistence, catalog references, filter
// queries, and route previews. Mounted components or pure helper calls exercise the real owning
// implementation, while controlled API and display dependencies expose the observable updates
// asserted by each scenario.

vi.mock('@/api/admin/modelAttributes', () => ({
  modelAttributesAPI: {
    list: vi.fn(),
    defaults: vi.fn(),
    getModelDefaultAttributes: vi.fn(),
    preview: vi.fn(),
    update: vi.fn(),
    save: vi.fn(),
    remove: vi.fn(),
  },
}))
vi.mock('@/api/admin', () => ({
  adminAPI: { groups: { getAll: vi.fn().mockResolvedValue([{ id: 7, name: 'Group' }]) } },
}))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))

// stubs supplies the display and dialog replacements used by these mounted tests, leaving the
// target component behavior active for assertions.
const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: {
    template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
  },
  DataTable: {
    props: ['data'],
    template:
      '<div><div v-for="row in data" :key="row.id || row.model"><span>{{ row.name || row.model }}</span><slot name="cell-actions" :row="row" /></div></div>',
  },
  BaseDialog: {
    props: ['show'],
    template: '<div v-if="show"><slot /><slot name="footer" /></div>',
  },
  RuleListEditor: {
    props: ['items'],
    template:
      '<div><div v-for="(item, index) in items" :key="index"><slot name="row" :item="item" :index="index" /></div></div>',
  },
  Icon: true,
  Select: true,
  Toggle: true,
  Pagination: true,
  ConfirmDialog: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockReset().mockResolvedValue({})
  vi.mocked(modelAttributesAPI.list).mockResolvedValue({
    items: [
      {
        id: 1,
        name: 'Profile',
        description: '',
        status: 'active',
        group_ids: [7],
        rules: [{ models: ['upstream'], attributes: { tool_call: false } }],
      },
    ],
    total: 1,
  })
  vi.mocked(modelAttributesAPI.defaults).mockResolvedValue({
    items: [{ model: 'default-keep', provider: 'original', source: 'models.dev', attributes: {} }],
    total: 1,
    providers: ['original'],
    version: 'abc123',
    last_updated: '2026-09-30T00:00:00Z',
  })
})

describe('属性管理页面', () => {
  // openEmptyRule mounts the attribute page and opens a draft containing a newly added rule. It
  // waits for page and dialog updates so the caller can interact with the rule fields.
  async function openEmptyRule() {
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    wrapper.getComponent(RuleListEditor).vm.$emit('add')
    await flushPromises()
    return wrapper
  }

  // A rule displays catalog attributes as reference before the user overrides them. Saving
  // retains explicit false and empty modalities from the draft rather than copying inherited
  // reference values.
  it('默认属性作为参考，保存时采用手动覆盖的 false 和空模态', async () => {
    const attributes = {
      display_name: 'Known',
      context: 128000,
      output_limit: 8192,
      reasoning: false,
      tool_call: true,
      input_modalities: ['text'],
      output_modalities: [],
    }
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockImplementation(async (model) =>
      model === 'vendor/known' ? attributes : {},
    )
    const wrapper = await openEmptyRule()
    const input = wrapper.findAll('input[aria-label="admin.modelAttributes.models"]')[1]!
    await input.setValue('vendor/known')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenCalledWith('vendor/known')
    const fields = wrapper.findAllComponents(ModelAttributesFields)[1]!
    expect(fields.props('modelValue')).toEqual({})
    expect(fields.props('inherited')).toEqual(attributes)
    fields.vm.$emit('update:modelValue', { reasoning: false, output_modalities: [] })
    await flushPromises()
    await wrapper.get('#attribute-form').trigger('submit')
    await flushPromises()
    expect(modelAttributesAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        rules: [
          expect.objectContaining({ models: ['upstream'], attributes: { tool_call: false } }),
          expect.objectContaining({
            id: expect.any(String),
            models: ['vendor/known'],
            attributes: { reasoning: false, output_modalities: [] },
          }),
        ],
      }),
    )
    wrapper.unmount()
  })

  // An existing rule is opened and its model tags are edited. Reference lookup runs for the
  // first concrete model and repeats when that reference identity changes.
  it('打开编辑时为已有规则查询参考，之后只在第一个非通配模型变化时重新查询', async () => {
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockResolvedValue({ reasoning: false })
    const wrapper = await openEmptyRule()
    expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenCalledExactlyOnceWith('upstream')
    expect(wrapper.findAllComponents(ModelAttributesFields)[0]!.props('inherited')).toEqual({
      reasoning: false,
    })
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockClear()

    const inputs = wrapper.findAll('input[aria-label="admin.modelAttributes.models"]')
    await inputs[0]!.setValue('another')
    await inputs[0]!.trigger('keydown', { key: 'Enter' })
    expect(modelAttributesAPI.getModelDefaultAttributes).not.toHaveBeenCalled()

    await inputs[1]!.trigger('paste', {
      clipboardData: { getData: () => 'claude-*,first,second\nfirst' },
    })
    await flushPromises()
    expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenCalledExactlyOnceWith('first')
    expect(wrapper.findAllComponents(ModelAttributesFields)[1]!.props('modelValue')).toEqual({})
    expect(wrapper.findAllComponents(ModelAttributesFields)[1]!.props('inherited')).toEqual({
      reasoning: false,
    })
    wrapper.unmount()
  })

  // A rule contains wildcard models or its reference request fails. Manual fields remain
  // editable, and wildcard-only rules skip a lookup that cannot identify a concrete catalog
  // model.
  it('通配符跳过查询，查询失败时仍可手动填写', async () => {
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockImplementation(async (model) => {
      if (model === 'offline') throw new Error('offline')
      return {}
    })
    const wrapper = await openEmptyRule()
    const input = wrapper.findAll('input[aria-label="admin.modelAttributes.models"]')[1]!
    for (const model of ['claude-*', 'offline']) {
      await input.setValue(model)
      await input.trigger('keydown', { key: 'Enter' })
      await flushPromises()
    }
    expect(modelAttributesAPI.getModelDefaultAttributes).not.toHaveBeenCalledWith('claude-*')
    expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenCalledWith('offline')
    const fields = wrapper.findAllComponents(ModelAttributesFields)[1]!
    expect(fields.props('inherited')).toBeUndefined()
    await fields.get('input').setValue('Manual')
    expect(fields.props('modelValue')).toMatchObject({
      display_name: 'Manual',
      display_name_localization: { source: 'Manual' },
    })
    wrapper.unmount()
  })

  // A reference request is pending while the model, rule, or editor session changes. Its
  // completion does not overwrite the active reference state asserted by the corresponding
  // scenario.
  it.each(['删除模型', '删除规则', '重新打开'])('%s 后丢弃仍在等待的参考属性', async (action) => {
    let resolve!: (attributes: ModelAttributes) => void
    const stale = { display_name: 'Stale', tool_call: true }
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockImplementation((model) =>
      model === 'pending'
        ? new Promise((done) => {
            resolve = done
          })
        : Promise.resolve({}),
    )
    const wrapper = await openEmptyRule()
    const input = wrapper.findAll('input[aria-label="admin.modelAttributes.models"]')[1]!
    await input.setValue('pending')
    await input.trigger('keydown', { key: 'Enter' })

    if (action === '删除模型')
      await wrapper.get('button[aria-label="common.delete pending"]').trigger('click')
    if (action === '删除规则') wrapper.getComponent(RuleListEditor).vm.$emit('remove', 1)
    if (action === '重新打开')
      await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    resolve(stale)
    await flushPromises()

    expect(
      wrapper.findAllComponents(ModelAttributesFields).map((fields) => fields.props('inherited')),
    ).not.toContainEqual(stale)
    wrapper.unmount()
  })

  // A previous reference model is removed before its slow request returns. A newer model
  // reference remains displayed so the earlier response cannot replace the current rule's
  // reference.
  it('参考模型被删除后，先发后到的结果不覆盖新的参考', async () => {
    let resolve!: (attributes: ModelAttributes) => void
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockImplementation((model) => {
      if (model === 'slow')
        return new Promise((done) => {
          resolve = done
        })
      return Promise.resolve(model === 'latest' ? { display_name: 'Latest' } : {})
    })
    const wrapper = await openEmptyRule()
    const input = wrapper.findAll('input[aria-label="admin.modelAttributes.models"]')[1]!
    for (const model of ['slow', 'latest']) {
      await input.setValue(model)
      await input.trigger('keydown', { key: 'Enter' })
    }
    await wrapper.get('button[aria-label="common.delete slow"]').trigger('click')
    await flushPromises()
    resolve({ display_name: 'Stale' })
    await flushPromises()
    expect(wrapper.findAllComponents(ModelAttributesFields)[1]!.props('modelValue')).toEqual({})
    expect(wrapper.findAllComponents(ModelAttributesFields)[1]!.props('inherited')).toEqual({
      display_name: 'Latest',
    })
    wrapper.unmount()
  })

  // Default-attribute filters are combined, counted, reset, and dismissed. Queries reflect the
  // chosen provider and capability, and the filter badge and panel follow the same selection
  // state.
  it('默认属性的筛选面板支持组合查询、计数、重置和关闭', async () => {
    const wrapper = mount(ModelAttributesView, { attachTo: document.body, global: { stubs } })
    await flushPromises()
    expect(wrapper.find('button[aria-label="common.filter"]').exists()).toBe(false)

    await wrapper.findAll('[role="tab"]')[1]!.trigger('click')
    await flushPromises()
    const filter = wrapper.get('button[aria-label="common.filter"]')
    expect(filter.attributes('aria-expanded')).toBe('false')
    expect(wrapper.findAllComponents({ name: 'FilterOptions' })).toHaveLength(0)
    await filter.trigger('click')

    // Changing both filters in one update should issue one query, with the active-filter count
    // matching the submitted constraints.
    vi.mocked(modelAttributesAPI.defaults).mockClear()
    const filterOptions = wrapper.findAllComponents({ name: 'FilterOptions' })
    filterOptions[0]!.vm.$emit('update:modelValue', 'original')
    filterOptions[1]!.vm.$emit('update:modelValue', 'tool_call')
    await flushPromises()
    expect(filter.text()).toBe('2')
    expect(modelAttributesAPI.defaults).toHaveBeenCalledTimes(1)
    expect(modelAttributesAPI.defaults).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 1, provider: 'original', capability: 'tool_call' }),
    )

    await wrapper.get('[data-testid="filter-reset"]').trigger('click')
    await flushPromises()
    expect(filter.text()).toBe('')
    expect(modelAttributesAPI.defaults).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 1, provider: '', capability: '' }),
    )

    await wrapper.get('.filter-panel').trigger('keydown', { key: 'Escape' })
    expect(filter.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(filter.element)
    await filter.trigger('click')
    document.body.click()
    await flushPromises()
    expect(filter.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  // Default attributes are queried and a manual catalog update fails. The page distinguishes
  // read-only loading from updating and retains the existing catalog rows with an error
  // message.
  it('默认属性页只读查询，更新失败保留现有目录', async () => {
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.findAll('[role="tab"]')[1]!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('default-keep')
    expect(modelAttributesAPI.update).not.toHaveBeenCalled()
    vi.mocked(modelAttributesAPI.update).mockRejectedValueOnce(new Error('offline'))
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.pricing.defaults.update')!
      .trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('default-keep')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    wrapper.unmount()
  })
  // A saved attribute configuration contains explicit false and group associations. Opening and
  // saving the editor retains both values so hydration cannot convert them into inheritance or
  // detach groups.
  it('编辑回显与保存保留显式 false 及分组关联', async () => {
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.find('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    await wrapper.find('#attribute-form').trigger('submit')
    await flushPromises()
    expect(modelAttributesAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 1,
        group_ids: [7],
        rules: [{ models: ['upstream'], attributes: { tool_call: false } }],
      }),
    )
    wrapper.unmount()
  })

  // An existing translated name is cleared before previewing and saving. An empty name inherits,
  // while retyping keeps the saved localization revision needed to update the existing rule.
  it.each(['', 'Renamed'])('清空显示名后以 %j 预览和保存', async (name) => {
    vi.mocked(modelAttributesAPI.list).mockResolvedValueOnce({
      items: [
        {
          id: 1,
          name: 'Profile',
          description: '',
          status: 'active',
          group_ids: [7],
          rules: [
            {
              id: 'existing-rule',
              models: ['upstream'],
              attributes: {
                tool_call: false,
                output_modalities: [],
                display_name: 'Custom',
                display_name_localization: {
                  ...originalContent('Custom', 'en'),
                  revision: 7,
                  source_revision: 3,
                  translations: { 'zh-Hans': { value: '自定义', source_revision: 3 } },
                },
              },
            },
          ],
        },
      ],
      total: 1,
    })
    vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mockResolvedValue({
      display_name: 'Catalog name',
    })
    vi.mocked(modelAttributesAPI.preview).mockResolvedValue({ routes: [] })
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    const fields = wrapper.getComponent(ModelAttributesFields)
    const input = fields.getComponent(LocalizedEditor).get('input')
    await input.setValue('')
    expect(input.attributes('placeholder')).toBe('admin.modelAttributes.inheritValue')
    if (name) await input.setValue(name)
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.modelAttributes.preview')!
      .trigger('click')
    await flushPromises()
    const expected = name
      ? {
          tool_call: false,
          output_modalities: [],
          display_name: name,
          display_name_localization: {
            ...originalContent(name, 'en'),
            revision: 7,
            source_revision: 3,
            reviewed_locales: [],
            translations: { 'zh-Hans': { value: '自定义', source_revision: -1 } },
          },
        }
      : { tool_call: false, output_modalities: [] }
    const previewConfig = vi.mocked(modelAttributesAPI.preview).mock.calls.at(-1)![2]!
    expect(previewConfig.rules[0]!.attributes).toEqual(expected)
    expect(fields.props('modelValue').display_name_localization?.revision).toBe(7)
    await wrapper.get('#attribute-form').trigger('submit')
    await flushPromises()
    const savedConfig = vi.mocked(modelAttributesAPI.save).mock.calls.at(-1)![0]
    expect(savedConfig.rules[0]!.attributes).toEqual(expected)
    wrapper.unmount()
  })

  // Model tags are added with Enter and later removed before saving. The submitted
  // configuration retains the resulting model array and selected groups from the same editor
  // draft.
  it('模型按回车添加标签，删除后保存仍提交模型数组和分组选择', async () => {
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()

    const input = wrapper.get('input[aria-label="admin.modelAttributes.models"]')
    await input.setValue('claude-*')
    await input.trigger('keydown', { key: 'Enter' })
    expect(modelAttributesAPI.save).not.toHaveBeenCalled()
    expect(wrapper.get('button[aria-label="common.delete claude-*"]').exists()).toBe(true)
    await wrapper.get('button[aria-label="common.delete upstream"]').trigger('click')
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('#attribute-form').trigger('submit')
    await flushPromises()

    expect(modelAttributesAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        group_ids: [],
        rules: [{ models: ['claude-*'], attributes: { tool_call: false } }],
      }),
    )
    wrapper.unmount()
  })

  // All model tags are removed from a rule before submission. The page blocks saving and
  // reports the model requirement so an empty rule cannot be persisted through the editor.
  it('清空模型标签后阻止保存空规则', async () => {
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    await wrapper.get('button[aria-label="common.delete upstream"]').trigger('click')
    await wrapper.get('#attribute-form').trigger('submit')
    await flushPromises()

    expect(modelAttributesAPI.save).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.modelAttributes.modelsRequired')
    wrapper.unmount()
  })
  // A rule includes route conditions and the draft is previewed for a group and model. Saved
  // conditions and displayed resolved attributes retain their route and source information.
  it('保存路线条件并展示草稿的最终属性与来源', async () => {
    vi.mocked(modelAttributesAPI.preview).mockResolvedValue({
      routes: [
        {
          target: {
            model: 'gpt-5.4',
            platform: 'openai',
            provider_type: 'apikey',
            catalog_provider: 'openai',
            client_protocol: 'openai_responses',
          },
          attributes: { temperature: true, conditions: { temperature: { efforts: ['none'] } } },
          sources: { temperature: 'config' },
          rules: {},
          version: 'fixed',
        },
      ],
    })
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    wrapper
      .getComponent(ModelAttributeRouteFields)
      .vm.$emit('update:modelValue', { platform: 'openai', provider_type: 'apikey' })
    await flushPromises()
    const preview = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.modelAttributes.preview')!
    await preview.trigger('click')
    await flushPromises()
    expect(modelAttributesAPI.preview).toHaveBeenCalledWith(
      7,
      'upstream',
      expect.objectContaining({
        rules: [
          expect.objectContaining({ match: { platform: 'openai', provider_type: 'apikey' } }),
        ],
      }),
    )
    expect(wrapper.text()).toContain('gpt-5.4')
    expect(wrapper.text()).toContain('admin.modelAttributes.sources.config')
    expect(wrapper.find('[data-capability="temperature"][data-state="conditional"]').exists()).toBe(
      true,
    )
    await wrapper.get('#attribute-form').trigger('submit')
    await flushPromises()
    expect(modelAttributesAPI.save).toHaveBeenCalledWith(
      expect.objectContaining({
        rules: [
          expect.objectContaining({ match: { platform: 'openai', provider_type: 'apikey' } }),
        ],
      }),
    )
    wrapper.unmount()
  })

  // A route preview completes before the configuration draft is changed. The draft watcher
  // clears the previous result so the panel cannot present it as the preview of the edited
  // configuration.
  it('草稿修改后清除旧的预览结果', async () => {
    vi.mocked(modelAttributesAPI.preview).mockResolvedValue({
      routes: [
        {
          target: { model: 'gpt-5.4', platform: 'openai' },
          attributes: {},
          sources: {},
          rules: {},
          version: 'fixed',
        },
      ],
    })
    const wrapper = mount(ModelAttributesView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.modelAttributes.preview')!
      .trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('gpt-5.4')

    wrapper
      .getComponent(ModelAttributeRouteFields)
      .vm.$emit('update:modelValue', { platform: 'gemini' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('gpt-5.4')
    wrapper.unmount()
  })
})

// Typing a reference updates the draft and loads defaults after the field change.
// Saving strips hidden attribute overrides and preserves the entered target.
// A rejected request leaves the editor open so the same draft can be retried.
it('saves references and retains the draft after failure', async () => {
  vi.mocked(modelAttributesAPI.list).mockResolvedValue({
    items: [
      {
        id: 1,
        name: 'Profile',
        description: '',
        status: 'active',
        group_ids: [7],
        rules: [{ id: 'rule', models: ['custom-*'], reference_model: 'target', attributes: {} }],
      },
    ],
    total: 1,
  })
  vi.mocked(modelAttributesAPI.save)
    .mockRejectedValueOnce(new Error('save failed'))
    .mockResolvedValue({} as never)
  const wrapper = mount(ModelAttributesView, { global: { stubs } })
  await flushPromises()
  const edit = wrapper
    .findAll('button')
    .find(
      (button) =>
        button.attributes('title') === 'common.edit' || button.text().includes('common.edit'),
    )
  expect(edit).toBeDefined()
  await edit!.trigger('click')
  await flushPromises()
  expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenCalledWith('target')
  const input = wrapper.get<HTMLInputElement>('input[aria-label="admin.pricing.referenceTarget"]')
  const lookups = vi.mocked(modelAttributesAPI.getModelDefaultAttributes).mock.calls.length
  input.element.value = 'manual-target'
  await input.trigger('input')
  expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenCalledTimes(lookups)
  await input.trigger('change')
  await flushPromises()
  expect(modelAttributesAPI.getModelDefaultAttributes).toHaveBeenLastCalledWith('manual-target')
  await wrapper.find('#attribute-form').trigger('submit')
  await flushPromises()
  expect(modelAttributesAPI.save).toHaveBeenLastCalledWith(
    expect.objectContaining({
      rules: [expect.objectContaining({ reference_model: 'manual-target', attributes: {} })],
    }),
  )
  expect(wrapper.find('#attribute-form').exists()).toBe(true)
  await wrapper.find('#attribute-form').trigger('submit')
  await flushPromises()
  expect(wrapper.find('#attribute-form').exists()).toBe(false)
})

// Switching an existing custom rule into reference mode keeps its draft fields but
// blocks saving until a target is selected. Returning to custom mode restores them.
it('requires a reference target and retains custom attributes through mode changes', async () => {
  const wrapper = mount(ModelAttributesView, { global: { stubs } })
  await flushPromises()
  await wrapper.get('button[aria-label="common.edit"]').trigger('click')
  await flushPromises()
  const mode = wrapper
    .findAllComponents({ name: 'Select' })
    .find((item) => item.attributes('aria-label') === 'admin.pricing.ruleMode')!
  expect(mode).toBeDefined()
  mode.vm.$emit('update:modelValue', 'reference')
  await flushPromises()
  await wrapper.get('#attribute-form').trigger('submit')
  await flushPromises()
  expect(modelAttributesAPI.save).not.toHaveBeenCalled()
  expect(wrapper.text()).toContain('admin.pricing.referenceInvalid')
  mode.vm.$emit('update:modelValue', 'custom')
  await flushPromises()
  expect(wrapper.findComponent(ModelAttributesFields).props('modelValue')).toEqual({
    tool_call: false,
  })
  wrapper.unmount()
})

// A persisted reference keeps its selected mode when its target is cleared for editing.
// Empty targets block preview and save; entering a replacement persists the reference again.
it.each(['', '  '])('keeps saved reference mode when the target becomes %j', async (empty) => {
  vi.mocked(modelAttributesAPI.list).mockResolvedValue({
    items: [
      {
        id: 1,
        name: 'Profile',
        description: '',
        status: 'active',
        group_ids: [7],
        rules: [{ id: 'alias', models: ['source'], reference_model: 'target', attributes: {} }],
      },
    ],
    total: 1,
  })
  vi.mocked(modelAttributesAPI.save).mockResolvedValue({} as never)
  const wrapper = mount(ModelAttributesView, { global: { stubs } })
  await flushPromises()
  await wrapper.get('button[aria-label="common.edit"]').trigger('click')
  await flushPromises()
  await wrapper.get('input[aria-label="admin.pricing.referenceTarget"]').setValue(empty)
  await flushPromises()
  const mode = wrapper
    .findAllComponents({ name: 'Select' })
    .find((item) => item.attributes('aria-label') === 'admin.pricing.ruleMode')!
  expect(mode.props('modelValue')).toBe('reference')
  expect(wrapper.find('input[aria-label="admin.pricing.referenceTarget"]').exists()).toBe(true)
  await wrapper.get('#attribute-form').trigger('submit')
  await flushPromises()
  expect(modelAttributesAPI.save).not.toHaveBeenCalled()
  expect(wrapper.text()).toContain('admin.pricing.referenceInvalid')
  await wrapper
    .findAll('button')
    .find((button) => button.text() === 'admin.modelAttributes.preview')!
    .trigger('click')
  await flushPromises()
  expect(modelAttributesAPI.preview).not.toHaveBeenCalled()
  await wrapper.get('input[aria-label="admin.pricing.referenceTarget"]').setValue('replacement')
  await flushPromises()
  await wrapper.get('#attribute-form').trigger('submit')
  await flushPromises()
  expect(modelAttributesAPI.save).toHaveBeenCalledWith(
    expect.objectContaining({
      rules: [{ id: 'alias', models: ['source'], reference_model: 'replacement', attributes: {} }],
    }),
  )
  wrapper.unmount()
})
