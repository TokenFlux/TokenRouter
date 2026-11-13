import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'

import FilterField from '../FilterField.vue'
import FilterOptions from '../FilterOptions.vue'
import type { SelectOption } from '../Select.vue'

// FilterOptions.spec.ts tests the checkable option rows of a filter field. Mounted lists
// cover hiding the unfiltered entry, choosing and clearing a single value, shortening
// long lists, local and created search rows, remote search results, and keyboard
// movement. A FilterField host supplies the explicit reset value where a case needs one.

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

// stubs replaces icons and collapse motion so rows render immediately.
const stubs = { Icon: true }

// statusOptions lists an unfiltered entry followed by three real status choices.
const statusOptions: SelectOption[] = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '启用' },
  { value: 'inactive', label: '停用' },
  { value: 'error', label: '异常' },
]

// mountList mounts a standalone option list with the supplied props.
const mountList = (props: Record<string, unknown>) =>
  mount(FilterOptions, {
    props: { modelValue: '', options: statusOptions, ...props },
    global: { stubs },
  })

// rowLabels returns the visible text of every rendered option row.
const rowLabels = (wrapper: { findAll: (selector: string) => Array<{ text(): string }> }) =>
  wrapper.findAll('[role="option"]').map((row) => row.text())

describe('FilterOptions', () => {
  // A status list starts unfiltered. The leading All entry is not rendered and no row
  // is checked, since an empty set of checks already means nothing is filtered.
  it('不渲染“全部”行，未选择时没有勾选行', () => {
    const wrapper = mountList({})
    expect(rowLabels(wrapper)).toEqual(['启用', '停用', '异常'])
    expect(wrapper.find('[aria-selected="true"]').exists()).toBe(false)
  })

  // The user picks a row and then picks the checked row again. The first click emits the
  // row value and option; the second restores the inferred empty value.
  it('点选写入取值，再点已选行恢复为空', async () => {
    const wrapper = mountList({ modelValue: 'active' })
    await wrapper.get('[data-value="inactive"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['inactive'])
    expect(wrapper.emitted('change')?.[0]).toEqual(['inactive', statusOptions[2]])

    await wrapper.get('[data-value="active"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual([''])
    expect(wrapper.emitted('change')?.[1]).toEqual(['', statusOptions[0]])
  })

  // A field declares a non-blank reset value, like a default time range. Every option
  // stays visible, and choosing the checked non-default row restores that reset value
  // while choosing the reset row itself changes nothing.
  it('显式 empty-value 时显示全部选项，取消选择回到该值', async () => {
    const ranges: SelectOption[] = [
      { value: '5m', label: '5 分钟' },
      { value: '1h', label: '1 小时' },
      { value: '6h', label: '6 小时' },
    ]
    const wrapper = mount(
      defineComponent({
        components: { FilterField, FilterOptions },
        // setup holds the selected range so emitted updates write back.
        setup: () => ({ range: ref('6h'), ranges }),
        template: `
          <FilterField label="时间" icon="clock" empty-value="1h">
            <FilterOptions v-model="range" :options="ranges" />
          </FilterField>
        `,
      }),
      { global: { stubs } },
    )
    expect(rowLabels(wrapper)).toEqual(['5 分钟', '1 小时', '6 小时'])
    await wrapper.get('[data-value="6h"]').trigger('click')
    expect((wrapper.vm as unknown as { range: string }).range).toBe('1h')

    await wrapper.get('[data-value="1h"]').trigger('click')
    expect((wrapper.vm as unknown as { range: string }).range).toBe('1h')
  })

  // A static list has more than six choices. Five rows show with a More toggle, which
  // expands the rest; a checked row beyond the first five expands the list on mount.
  it('超过 6 项时先显示 5 项和“更多”，选中项靠后时自动展开', async () => {
    const many: SelectOption[] = [
      { value: null, label: '全部' },
      ...Array.from({ length: 8 }, (_, i) => ({ value: `v${i}`, label: `选项${i}` })),
    ]
    const collapsed = mountList({ modelValue: null, options: many })
    expect(rowLabels(collapsed)).toHaveLength(5)
    const more = collapsed.get('button[aria-expanded]')
    expect(more.text()).toBe('common.more')
    await more.trigger('click')
    expect(rowLabels(collapsed)).toHaveLength(8)
    expect(more.text()).toBe('common.collapse')

    const expanded = mountList({ modelValue: 'v7', options: many })
    expect(rowLabels(expanded)).toHaveLength(8)
  })

  // A searchable list receives text. Rows filter by label and search aliases, and a
  // query without matches shows the empty message instead of rows.
  it('searchable 按标签和别名过滤，没有结果时提示', async () => {
    const groups: SelectOption[] = [
      { value: null, label: '全部分组' },
      { value: 1, label: 'Claude', search_terms: ['克劳德'] },
      { value: 2, label: 'OpenAI' },
    ]
    const wrapper = mountList({ modelValue: null, options: groups, searchable: true })
    const input = wrapper.get('input')
    await input.setValue('克劳')
    expect(rowLabels(wrapper)).toEqual(['Claude'])
    await input.setValue('none')
    expect(wrapper.find('[role="option"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('common.noOptionsFound')
  })

  // A creatable list receives text that matches no option. A created row offers that
  // text as the value, and a created value stays checked at the top after selection.
  it('creatable 把输入作为可选行，选中后固定在顶部', async () => {
    const wrapper = mountList({
      modelValue: null,
      options: [{ value: 'gpt-4o', label: 'gpt-4o' }],
      searchable: true,
      creatable: true,
    })
    await wrapper.get('input').setValue('claude-x')
    expect(rowLabels(wrapper)[0]).toBe('common.search "claude-x"')
    await wrapper.get('[data-value="claude-x"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['claude-x'])

    await wrapper.setProps({ modelValue: 'claude-x' })
    await wrapper.get('input').setValue('')
    expect(rowLabels(wrapper)).toEqual(['claude-x', 'gpt-4o'])
    expect(wrapper.get('[aria-selected="true"]').text()).toBe('claude-x')
  })

  // A remote list receives text and results that exclude the current selection. Typing
  // emits search without filtering locally, and the selection stays visible as a checked
  // row named by selectedLabel above the results.
  it('remote 发出 search，已选值不在结果中时补在顶部', async () => {
    const wrapper = mountList({
      modelValue: 7,
      options: [{ value: 8, label: 'other@test.com' }],
      remote: true,
      selectedLabel: 'picked@test.com',
    })
    expect(rowLabels(wrapper)).toEqual(['picked@test.com', 'other@test.com'])
    expect(wrapper.get('[aria-selected="true"]').text()).toBe('picked@test.com')

    await wrapper.get('input').setValue('zzz')
    expect(wrapper.emitted('search')?.at(-1)).toEqual(['zzz'])
    expect(rowLabels(wrapper)).toEqual(['picked@test.com', 'other@test.com'])

    await wrapper.get('[aria-selected="true"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([null])
  })

  // A caller supplies the icon slot and leaves one row's icon empty. Every row still
  // reserves the icon column, the slot receives each row's value, and the pinned row of
  // a value missing from the options also receives its value.
  it('icon 插槽为每行占位并收到行的取值', () => {
    const wrapper = mount(FilterOptions, {
      props: { modelValue: 'custom', options: statusOptions, selectedLabel: '自定义' },
      slots: {
        icon: `<template #icon="{ value }"><i v-if="value !== 'error'" :data-icon="value" /></template>`,
      },
      global: { stubs },
    })
    const rows = wrapper.findAll('[role="option"]')
    expect(rows.map((row) => row.findAll('.filter-option-icon').length)).toEqual([1, 1, 1, 1])
    expect(wrapper.findAll('[data-icon]').map((icon) => icon.attributes('data-icon'))).toEqual([
      'custom',
      'active',
      'inactive',
    ])
  })

  // Without the icon slot, rows render no icon column and labels sit next to the check.
  it('没有 icon 插槽时不占位', () => {
    const wrapper = mountList({})
    expect(wrapper.find('.filter-option-icon').exists()).toBe(false)
  })

  // Keyboard users move through rows with the arrow keys, Home, and End. Focus moves
  // without selecting, and only one row stays in the tab order at a time.
  it('方向键、Home、End 只移动焦点不选择', async () => {
    const wrapper = mount(FilterOptions, {
      props: { modelValue: 'inactive', options: statusOptions },
      global: { stubs },
      attachTo: document.body,
    })
    const rows = wrapper.findAll<HTMLButtonElement>('[role="option"]')
    expect(rows.map((row) => row.attributes('tabindex'))).toEqual(['-1', '0', '-1'])

    rows[1]!.element.focus()
    await rows[1]!.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(rows[2]!.element)
    await rows[2]!.trigger('keydown', { key: 'Home' })
    expect(document.activeElement).toBe(rows[0]!.element)
    await rows[0]!.trigger('keydown', { key: 'End' })
    expect(document.activeElement).toBe(rows[2]!.element)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })
})
