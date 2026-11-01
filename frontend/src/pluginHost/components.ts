import { defineComponent, h, type Component, type PropType } from 'vue'
import type {
  BadgeTone,
  ButtonProps,
  DialogProps,
  IconName,
  SelectOption,
  TableColumn,
} from '@tokenflux/tokenrouter-plugin-sdk'
import Input from '@/components/common/Input.vue'
import TextArea from '@/components/common/TextArea.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import BalanceAmount from '@/components/common/BalanceAmount.vue'
import Icon from '@/components/icons/Icon.vue'
import { useHost } from './context'

// components.ts maps the stable plugin component contract onto native controls.
// Wrappers select documented props and events while sharing host styling, focus
// handling, table behavior, currency formatting, and accessibility conventions.
// Presentation attributes pass through without exposing unrelated native component
// props to plugin authors. The host remains the owner of those implementation details.
//
// Dialog content leaves the page subtree through Teleport. Its native panel receives
// the same plugin identity as the page root, preserving the SDK's compiled style
// scope without copying the dialog implementation or changing its focus lifecycle.
// Other wrappers translate names and slot contracts at this boundary only.

// These maps select existing host recipes; plugin variants never introduce a
// separate color palette, icon registry, or dialog sizing convention.
const buttonClasses = {
  primary: 'btn-primary',
  secondary: 'btn-secondary',
  ghost: 'btn-ghost',
  danger: 'btn-danger',
}
const badgeClasses = {
  neutral: 'badge-gray',
  success: 'badge-success',
  warning: 'badge-warning',
  danger: 'badge-danger',
  brand: 'badge-primary',
}
const dialogWidths = { sm: 'narrow', md: 'normal', lg: 'wide' } as const
const iconNames = {
  check: 'check',
  calendar: 'calendar',
  gift: 'gift',
  refresh: 'refresh',
  cog: 'cog',
  x: 'x',
  info: 'infoCircle',
  user: 'user',
} as const

/** presentationAttrs forwards styling and accessible labels without exposing native control props.
 * Call during rendering so updated parent attributes remain reactive. Event listeners
 * are deliberately supplied by each wrapper's declared contract instead of this projection.
 */
function presentationAttrs(attrs: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(attrs).filter(
      ([key]) =>
        ['class', 'style', 'id', 'title', 'role'].includes(key) ||
        key.startsWith('aria-') ||
        key.startsWith('data-'),
    ),
  )
}

// The exported wrappers implement SDK props through native controls and retain
// parent-owned values; update events request changes without mutating plugin state.
export const TrButton = defineComponent({
  name: 'TrButton',
  inheritAttrs: false,
  props: {
    variant: { type: String as PropType<ButtonProps['variant']>, default: 'primary' }, // Host button recipe.
    size: { type: String as PropType<ButtonProps['size']>, default: 'md' }, // Host control size.
    loading: Boolean, // Pending action indicator.
    disabled: Boolean, // Action interaction availability.
  },
  emits: ['click'],
  /** setup renders a native button with loading admission and declared click delivery. */
  setup(props, { attrs, slots, emit }) {
    return () =>
      h(
        'button',
        {
          ...presentationAttrs(attrs),
          type: 'button',
          class: [
            'btn',
            buttonClasses[props.variant ?? 'primary'],
            props.size === 'sm' && 'btn-sm',
            attrs.class,
          ],
          disabled: props.disabled || props.loading,
          'aria-busy': props.loading || undefined,
          onClick: (event: MouseEvent) => emit('click', event),
        },
        [
          props.loading && h('span', { class: 'spinner h-4 w-4', 'aria-hidden': 'true' }),
          slots.default?.(),
        ],
      )
  },
})

export const TrCard = defineComponent({
  name: 'TrCard',
  inheritAttrs: false,
  /** setup places public header, body, and footer slots inside the native card surface. */
  setup(_, { attrs, slots }) {
    return () =>
      h('section', { ...presentationAttrs(attrs), class: ['card p-4 sm:p-6', attrs.class] }, [
        slots.header &&
          h(
            'div',
            { class: 'mb-4 border-b border-primary-900/10 pb-4 dark:border-dark-600' },
            slots.header(),
          ),
        slots.default?.(),
        slots.footer &&
          h(
            'div',
            { class: 'mt-4 border-t border-primary-900/10 pt-4 dark:border-dark-600' },
            slots.footer(),
          ),
      ])
  },
})

/** textControl maps the shared string-binding contract to either native text control.
 * Input and textarea keep identical plugin props and string update events. Native
 * maxlength handling remains on the actual editable element, including after rerenders.
 */
function textControl(component: Component, name: string) {
  return defineComponent({
    name,
    inheritAttrs: false,
    props: {
      modelValue: { type: String, default: '' }, // Parent-owned editable text.
      placeholder: String, // Empty control hint.
      disabled: Boolean, // Editing availability flag.
      maxlength: Number, // Native text length limit.
    },
    emits: ['update:modelValue'],
    /** setup binds string edits and native text limits to the selected editable control. */
    setup(props, { attrs, emit }) {
      return () =>
        h(component, {
          ...presentationAttrs(attrs),
          ...props,
          'onUpdate:modelValue': (value: string) => emit('update:modelValue', value),
        })
    },
  })
}

// Single-line and multiline controls share the SDK text binding and native
// maxlength forwarding, while their editing behaviors remain owned by the host.
export const TrInput = textControl(Input, 'TrInput')
export const TrTextarea = textControl(TextArea, 'TrTextarea')

export const TrSelect = defineComponent({
  name: 'TrSelect',
  inheritAttrs: false,
  props: {
    modelValue: [String, Number], // Parent-owned selected value.
    options: { type: Array as PropType<SelectOption[]>, required: true }, // Available labeled choices.
    disabled: Boolean, // Selection interaction availability.
  },
  emits: ['update:modelValue'],
  /** setup projects labeled options and forwards only supported string or numeric selections. */
  setup(props, { attrs, emit }) {
    return () =>
      h(Select, {
        ...presentationAttrs(attrs),
        ...props,
        options: props.options.map((option) => ({ ...option })),
        'onUpdate:modelValue': (value) => {
          if (typeof value === 'string' || typeof value === 'number')
            emit('update:modelValue', value)
        },
      })
  },
})

export const TrToggle = defineComponent({
  name: 'TrToggle',
  inheritAttrs: false,
  props: {
    modelValue: Boolean, // Parent-owned boolean value.
    disabled: Boolean, // Switch interaction availability.
  },
  emits: ['update:modelValue'],
  /** setup maps the controlled boolean value and native switch update event. */
  setup(props, { attrs, emit }) {
    return () =>
      h(Toggle, {
        ...presentationAttrs(attrs),
        ...props,
        'onUpdate:modelValue': (value) => emit('update:modelValue', value),
      })
  },
})

export const TrDialog = defineComponent({
  name: 'TrDialog',
  inheritAttrs: false,
  props: {
    open: { type: Boolean, required: true }, // Parent-owned visibility value.
    title: { type: String, required: true }, // Accessible dialog title.
    width: { type: String as PropType<DialogProps['width']>, default: 'md' }, // Host panel size.
  },
  emits: ['update:open'],
  /** setup binds native dialog visibility and teleported style identity to this plugin.
   * Mount beneath a PluginMount provider so useHost can resolve the owner. Native
   * dialog lifecycle continues to manage focus, scroll locking, and dismissal. */
  setup(props, { slots, emit }) {
    const host = useHost()
    return () =>
      h(
        BaseDialog,
        {
          show: props.open,
          title: props.title,
          width: dialogWidths[props.width ?? 'md'],
          panelData: { 'data-tr-plugin': host.pluginId },
          onClose: () => emit('update:open', false),
        },
        slots,
      )
  },
})

export const TrTable = defineComponent({
  name: 'TrTable',
  inheritAttrs: false,
  props: {
    columns: { type: Array as PropType<TableColumn[]>, required: true }, // Ordered column definitions.
    rows: { type: Array as PropType<Record<string, unknown>[]>, required: true }, // Current page records.
    loading: Boolean, // Pending request indicator.
  },
  /** setup translates SDK column titles and widths while retaining native cell slots. */
  setup(props, { attrs, slots }) {
    return () =>
      h(
        DataTable,
        {
          ...presentationAttrs(attrs),
          columns: props.columns.map((column) => ({
            key: column.key,
            label: column.title,
            width: column.width,
          })),
          data: props.rows,
          loading: props.loading,
        },
        slots,
      )
  },
})

export const TrPagination = defineComponent({
  name: 'TrPagination',
  inheritAttrs: false,
  props: {
    page: { type: Number, required: true }, // One-based page index.
    pageSize: { type: Number, required: true }, // Records per page.
    total: { type: Number, required: true }, // Total matching records.
  },
  emits: ['update:page'],
  /** setup exposes page navigation with the SDK-owned page size kept fixed. */
  setup(props, { attrs, emit }) {
    return () =>
      h(Pagination, {
        ...presentationAttrs(attrs),
        ...props,
        showPageSizeSelector: false,
        'onUpdate:page': (value) => emit('update:page', value),
      })
  },
})

export const TrEmpty = defineComponent({
  name: 'TrEmpty',
  inheritAttrs: false,
  props: {
    title: { type: String, required: true }, // Empty result heading.
    description: String, // Optional supporting explanation.
  },
  /** setup renders native empty-state content and the plugin-provided action slot. */
  setup(props, { attrs, slots }) {
    return () => h(EmptyState, { ...presentationAttrs(attrs), ...props }, slots)
  },
})

export const TrSkeleton = defineComponent({
  name: 'TrSkeleton',
  inheritAttrs: false,
  props: { rows: { type: Number, default: 3 } }, // Visible placeholder count.
  /** setup renders native loading placeholders using the requested row count. */
  setup(props, { attrs }) {
    return () => h(ContentSkeleton, { ...presentationAttrs(attrs), rows: props.rows })
  },
})

export const TrIcon = defineComponent({
  name: 'TrIcon',
  inheritAttrs: false,
  props: { name: { type: String as PropType<IconName>, required: true } }, // SDK common icon.
  /** setup selects an allowed common icon from the existing host registry. */
  setup(props, { attrs }) {
    return () => h(Icon, { ...presentationAttrs(attrs), name: iconNames[props.name], size: 'sm' })
  },
})

export const TrBadge = defineComponent({
  name: 'TrBadge',
  inheritAttrs: false,
  props: { tone: { type: String as PropType<BadgeTone>, default: 'neutral' } }, // Semantic status recipe.
  /** setup renders slot content with the native semantic badge recipe. */
  setup(props, { attrs, slots }) {
    return () =>
      h(
        'span',
        { ...presentationAttrs(attrs), class: ['badge', badgeClasses[props.tone], attrs.class] },
        slots.default?.(),
      )
  },
})

export const TrAmount = defineComponent({
  name: 'TrAmount',
  inheritAttrs: false,
  props: { value: { type: String, required: true } }, // Internal decimal amount.
  /** setup formats decimal display values through the native balance component. */
  setup(props, { attrs }) {
    // Native balance presentation accepts numbers; conversion is display-only and
    // never feeds the plugin's exact decimal input back into a financial request.
    return () => h(BalanceAmount, { ...presentationAttrs(attrs), amount: Number(props.value) })
  },
})
