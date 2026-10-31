import { defineComponent, h, onMounted } from 'vue'

// components.ts supplies native-element stand-ins for the local development host.
// These components preserve SDK bindings and events without importing TokenRouter
// application code into the MIT SDK. Production pages receive the real host
// wrappers through the externalized SDK global instead of these stand-ins.

// Native stand-ins keep the documented component names available to local pages.
// Their simple rendering supports author tests without claiming host visual parity.
export const TrButton = defineComponent({
  props: ['variant', 'size', 'loading', 'disabled'],
  emits: ['click'],
  /** setup binds pending and disabled states before forwarding enabled button clicks. */
  setup(props, { slots, emit }) {
    return () =>
      h(
        'button',
        {
          type: 'button',
          class: ['tr-dev-button', props.variant ?? 'primary'],
          disabled: props.disabled || props.loading,
          'aria-busy': props.loading || undefined,
          onClick: (event: MouseEvent) => emit('click', event),
        },
        props.loading ? '…' : slots.default?.(),
      )
  },
})
export const TrCard = defineComponent({
  /** setup renders the public header, body, and footer slots in order. */
  setup(_, { slots }) {
    return () =>
      h('section', { class: 'tr-dev-card' }, [
        slots.header ? h('header', slots.header()) : null,
        slots.default?.(),
        slots.footer ? h('footer', slots.footer()) : null,
      ])
  },
})
export const TrInput = textControl('input')
export const TrTextarea = textControl('textarea')
export const TrSelect = defineComponent({
  props: ['modelValue', 'options', 'disabled'],
  emits: ['update:modelValue'],
  /** setup preserves numeric option values when native selection events return strings. */
  setup(props, { emit }) {
    return () =>
      h(
        'select',
        {
          value: props.modelValue,
          disabled: props.disabled,
          onChange: (event: Event) => {
            const value = (event.target as HTMLSelectElement).value
            const option = props.options?.find(
              (item: { value: string | number }) => String(item.value) === value,
            )
            emit('update:modelValue', option?.value ?? value)
          },
        },
        props.options?.map((option: { value: string | number; label: string }) =>
          h('option', { value: option.value }, option.label),
        ),
      )
  },
})
export const TrToggle = defineComponent({
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue'],
  /** setup forwards checkbox changes through the public boolean binding event. */
  setup(props, { emit }) {
    return () =>
      h('input', {
        type: 'checkbox',
        role: 'switch',
        checked: props.modelValue,
        disabled: props.disabled,
        onChange: (event: Event) =>
          emit('update:modelValue', (event.target as HTMLInputElement).checked),
      })
  },
})
export const TrDialog = defineComponent({
  props: ['open', 'title', 'width'],
  emits: ['update:open'],
  /** setup renders visible dialog contents and forwards its close action. */
  setup(props, { slots, emit }) {
    return () =>
      props.open
        ? h('section', { role: 'dialog', 'aria-label': props.title, class: 'tr-dev-card' }, [
            h('header', [
              h('strong', props.title),
              h(
                'button',
                {
                  type: 'button',
                  onClick: () => emit('update:open', false),
                  'aria-label': 'Close',
                },
                '×',
              ),
            ]),
            slots.default?.(),
            slots.footer?.(),
          ])
        : null
  },
})
export const TrTable = defineComponent({
  props: ['columns', 'rows', 'loading'],
  /** setup renders column headers and named cell slots for supplied rows. */
  setup(props, { slots }) {
    return () =>
      h('table', { 'aria-busy': props.loading || undefined }, [
        h(
          'thead',
          h(
            'tr',
            props.columns?.map((column: { key: string; title: string; width?: string }) =>
              h('th', { style: { width: column.width } }, column.title),
            ),
          ),
        ),
        h(
          'tbody',
          props.rows?.map((row: Record<string, unknown>, index: number) =>
            h(
              'tr',
              { key: index },
              props.columns?.map((column: { key: string }) =>
                h(
                  'td',
                  slots[`cell-${column.key}`]?.({
                    row,
                    value: row[column.key],
                  }) ?? String(row[column.key] ?? ''),
                ),
              ),
            ),
          ),
        ),
      ])
  },
})
export const TrPagination = defineComponent({
  props: ['page', 'pageSize', 'total'],
  emits: ['update:page'],
  /** setup bounds previous and next actions using the supplied record total. */
  setup(props, { emit }) {
    return () =>
      h('nav', { 'aria-label': 'Pagination' }, [
        h(
          'button',
          {
            disabled: props.page <= 1,
            onClick: () => emit('update:page', props.page - 1),
          },
          'Previous',
        ),
        h('span', ` ${props.page} `),
        h(
          'button',
          {
            disabled: props.page * props.pageSize >= props.total,
            onClick: () => emit('update:page', props.page + 1),
          },
          'Next',
        ),
      ])
  },
})
export const TrEmpty = defineComponent({
  props: ['title', 'description'],
  /** setup renders the empty-state explanation and optional recovery action slot. */
  setup(props, { slots }) {
    return () => h('section', [h('h2', props.title), h('p', props.description), slots.action?.()])
  },
})
export const TrSkeleton = defineComponent({
  props: ['rows'],
  /** setup renders the requested number of local loading placeholder rows. */
  setup(props) {
    return () =>
      h(
        'div',
        { role: 'status', 'aria-label': 'Loading' },
        Array.from({ length: props.rows ?? 3 }, (_, i) =>
          h('p', { key: i, class: 'tr-dev-skeleton' }, ' '),
        ),
      )
  },
})
export const TrIcon = defineComponent({
  props: ['name'],
  /** setup exposes a diagnostic icon name without copying host artwork implementations. */
  setup(props) {
    return () => h('span', { 'aria-hidden': 'true', title: props.name }, '•')
  },
})
export const TrBadge = defineComponent({
  props: ['tone'],
  /** setup retains the semantic tone and renders the supplied badge content. */
  setup(props, { slots }) {
    return () => h('span', { class: ['tr-dev-badge', props.tone] }, slots.default?.())
  },
})
export const TrCaptcha = defineComponent({
  emits: ['verified', 'expired'],
  /** setup emits the disabled-site verification result when the mock component mounts. */
  setup(_, { emit }) {
    onMounted(() => emit('verified', ''))
    return () => null
  },
})
export const TrAmount = defineComponent({
  props: ['value'],
  /** setup formats mock amounts using the development site's declared USD unit. */
  setup(props) {
    return () =>
      h(
        'span',
        new Intl.NumberFormat(undefined, {
          style: 'currency',
          currency: 'USD',
          maximumFractionDigits: 8,
        }).format(Number(props.value)),
      )
  },
})

/** textControl creates a native text binding used by both input stand-ins. */
function textControl(tag: 'input' | 'textarea') {
  return defineComponent({
    props: ['modelValue', 'placeholder', 'disabled', 'maxlength'],
    emits: ['update:modelValue'],
    /** setup maps native text edits to the shared input binding contract. */
    setup(props, { emit }) {
      return () =>
        h(tag, {
          value: props.modelValue,
          placeholder: props.placeholder,
          disabled: props.disabled,
          maxlength: props.maxlength,
          onInput: (event: Event) =>
            emit('update:modelValue', (event.target as HTMLInputElement).value),
        })
    },
  })
}
