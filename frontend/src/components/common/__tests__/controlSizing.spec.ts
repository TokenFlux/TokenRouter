import { defineComponent, h } from 'vue'
import { mount, enableAutoUnmount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import postcss from 'postcss'
import ts from 'typescript'
import {
  applyTokens,
  classes,
  cssRules,
  directive,
  nodes,
  parseScript,
  parseVue,
  readSource,
} from '@/__tests__/helpers/sourceSyntax'
import Pagination from '../Pagination.vue'

// controlSizing.spec.ts checks shared control recipes and their component users.
// Parsed template classes and stylesheet rules bind size assertions to actual
// elements and selectors, while a mounted pagination fixture checks every visible
// button without depending on source quotes, line breaks, or attribute ordering.
// Pagination is the one compact exception: its 28px controls keep the bar at the
// height of the table header above it.

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

// SelectStub keeps pagination independent of dropdown interaction details; Pagination
// sizes the real trigger through its scoped style, which a separate test checks.
const SelectStub = defineComponent({
  name: 'PaginationSelectStub',
  props: ['modelValue', 'options'],
  // setup renders the selected page size as a plain trigger button.
  setup(props) {
    return () => h('button', { class: 'select-trigger' }, String(props.modelValue))
  },
})

// hasClasses reports whether one template element declares all requested utility classes.
// Bound class expressions contribute their string literals; callers use this for
// recipe presence and inspect conditional expressions separately when state matters.
function hasClasses(path: string, expected: string[]): boolean {
  return parseVue(path).elements.some((element) => {
    const binding = directive(element, 'bind', 'class')
    const dynamic = binding
      ? nodes(parseScript(`const classes = ${binding}`), ts.isStringLiteral).flatMap((node) =>
          node.text.split(/\s+/),
        )
      : []
    const tokens = [...classes(element), ...dynamic]
    return expected.every((token) => tokens.includes(token))
  })
}

describe('36px control sizing', () => {
  // Shared controls derive geometry from the button and input recipes. Each
  // selector must retain the relevant size, spacing, and typography utility tokens.
  it('defines the 36px baseline on the owning shared style rules', () => {
    const style = postcss.parse(readSource('style.css'))
    for (const [selector, expected] of [
      ['.btn', ['rounded-control', 'px-4', 'py-1.5', 'text-sm', 'font-medium', 'min-h-9']],
      [
        '.btn-icon',
        ['inline-flex', 'h-9', 'w-9', 'items-center', 'justify-center', 'rounded-control', 'p-0'],
      ],
      ['.input', ['w-full', 'rounded-control', 'px-4', 'py-1.5', 'text-sm', 'min-h-9']],
      ['.sidebar-link', ['flex', 'h-9', 'items-center', 'gap-3', 'rounded-control', 'py-1.5']],
    ] as const) {
      expect(cssRules(style, selector).flatMap(applyTokens)).toEqual(
        expect.arrayContaining(expected),
      )
    }
  })

  // Dropdown triggers inherit their dimensions from the common input recipe.
  // Local duplicate size recipes would diverge when the shared baseline changes.
  it.each([
    'components/common/Select.vue',
    'components/admin/proxy/ProxySelector.vue',
    'components/common/DateRangePicker.vue',
  ])('%s uses the shared trigger recipe', (path) => {
    expect(hasClasses(path, ['input', 'input-trigger'])).toBe(true)
    parseVue(path).styles.walkRules((rule) => {
      expect(applyTokens(rule)).not.toEqual(
        expect.arrayContaining(['h-9', 'min-h-9', 'rounded-control', 'px-4', 'py-1.5', 'text-sm']),
      )
    })
  })

  // Date confirmation and the pagination jump action both build on the shared compact
  // button and set only a height utility. Neither keeps a private size recipe.
  it('uses shared confirmation and pagination button recipes', () => {
    expect(
      hasClasses('components/common/DateRangePicker.vue', ['btn', 'btn-primary', 'btn-sm', 'h-8']),
    ).toBe(true)
    expect(
      cssRules(parseVue('components/common/DateRangePicker.vue').styles, '.date-picker-apply'),
    ).toEqual([])
    expect(
      hasClasses('components/common/Pagination.vue', [
        'pagination-jump-button',
        'btn',
        'btn-ghost',
        'btn-sm',
        'h-7',
      ]),
    ).toBe(true)
    const heightOverrides: string[] = []
    parseVue('components/common/Pagination.vue').styles.walkDecls(
      '--pagination-control-height',
      (node) => {
        heightOverrides.push(node.value)
      },
    )
    expect(heightOverrides).toEqual([])
  })

  // A middle page displays mobile and desktop navigation plus numbered pages. Every
  // pagination button must use the 28px utility so the bar matches the header row.
  it('renders every pagination button at the compact 28px height', () => {
    const wrapper = mount(Pagination, {
      props: { total: 30, page: 2, pageSize: 10 },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })
    const buttons = wrapper.findAll('button.pagination-control')
    expect(buttons).toHaveLength(7)
    expect(buttons.every((button) => button.classes().includes('h-7'))).toBe(true)
  })

  // The page-size trigger and jump input otherwise inherit the 36px input baseline.
  // Pagination must lower both to 28px, or the bar grows past the header height.
  it('lowers the pagination select trigger and jump input to 28px', () => {
    const styles = parseVue('components/common/Pagination.vue').styles
    for (const selector of ['.page-size-select :deep(.input)', '.pagination-jump-input']) {
      expect(cssRules(styles, selector).flatMap(applyTokens)).toEqual(
        expect.arrayContaining(['h-7', 'min-h-7', 'py-0']),
      )
    }
  })

  // Page-level controls compose the common recipes with their local layout needs.
  // Matching tokens on the same element protects sizing through template formatting.
  it.each([
    ['views/user/UsageView.vue', ['card', 'p-4']],
    ['views/admin/UsageView.vue', ['card', 'p-4']],
    [
      'views/admin/RiskControlView.vue',
      [
        'grid',
        'grid-cols-1',
        'items-start',
        'gap-4',
        'p-4',
        'xl:grid-cols-[minmax(0,1fr)_minmax(360px,440px)]',
      ],
    ],
    ['views/admin/settings/AdminApiKeySettings.vue', ['btn', 'btn-primary', 'btn-sm', 'h-9']],
    [
      'views/admin/settings/IdentitySettings.vue',
      ['btn', 'btn-secondary', 'btn-sm', 'h-9', 'shrink-0'],
    ],
    ['views/admin/settings/EmailTemplateEditor.vue', ['btn', 'btn-secondary', 'btn-sm', 'h-9']],
    ['views/admin/settings/BackupSettings.vue', ['btn', 'btn-primary', 'btn-sm', 'h-9']],
    ['components/admin/payment/PaymentProviderList.vue', ['btn', 'btn-secondary', 'btn-icon']],
  ] as const)('%s uses the expected page control classes', (path, expected) => {
    expect(hasClasses(path, [...expected])).toBe(true)
  })

  // Expanded API-key rows need room for their contents outside the collapsed
  // region. The class binding applies overflow visibility only to the expanded branch.
  it('ties risk-control overflow visibility to row expansion', () => {
    const expressions = parseVue('views/admin/RiskControlView.vue').elements.flatMap((element) => {
      const expression = directive(element, 'bind', 'class')
      return expression
        ? nodes(parseScript(`const value = ${expression}`), ts.isConditionalExpression)
        : []
    })
    expect(
      expressions.some(
        (node) =>
          ts.isIdentifier(node.condition) &&
          node.condition.text === 'apiKeyRowsExpanded' &&
          ts.isStringLiteral(node.whenTrue) &&
          node.whenTrue.text === 'overflow-visible' &&
          ts.isStringLiteral(node.whenFalse) &&
          node.whenFalse.text === '',
      ),
    ).toBe(true)
  })

  // Admin order and plan lists place their table content in the shared layout.
  // The named table slot must belong to that component's actual child template.
  it.each([
    'views/admin/orders/AdminOrdersView.vue',
    'views/admin/orders/AdminPaymentPlansView.vue',
  ])('%s uses the shared table slot', (path) => {
    const layout = parseVue(path).elements.find((element) => element.tag === 'TablePageLayout')
    expect(layout).toBeDefined()
    expect(
      layout?.children.some(
        (child) =>
          child.type === 1 &&
          child.tag === 'template' &&
          child.props.some(
            (prop) =>
              prop.type === 7 &&
              prop.name === 'slot' &&
              prop.arg?.type === 4 &&
              prop.arg.content === 'table',
          ),
      ),
    ).toBe(true)
  })
})
