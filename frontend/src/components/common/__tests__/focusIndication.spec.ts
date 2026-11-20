import postcss from 'postcss'
import { describe, expect, it } from 'vitest'
import {
  applyTokens,
  classes,
  cssRules,
  parseVue,
  readSource,
} from '@/__tests__/helpers/sourceSyntax'

// focusIndication.spec.ts checks how shared buttons, switches, form fields, and
// dropdown triggers indicate focus. Pointer clicks on buttons and triggers must
// leave no ring around the control, so those recipes draw focus only through
// focus-visible outline utilities that browsers apply during keyboard navigation.
// Text fields mark focus by strengthening their border, and no recipe draws an
// outer ring halo. Parsed stylesheet rules and template classes keep the
// assertions independent of quoting, wrapping, and attribute order.

// KEYBOARD_OUTLINE lists the utilities that draw the keyboard-only focus outline.
const KEYBOARD_OUTLINE = [
  'focus-visible:outline',
  'focus-visible:outline-2',
  'focus-visible:outline-offset-2',
  'focus-visible:outline-primary-600',
  'dark:focus-visible:outline-primary-500',
]

// BUTTON_SELECTORS covers the base button recipe and every variant that could
// reintroduce pointer focus styling on top of it.
const BUTTON_SELECTORS = [
  '.btn',
  '.btn-primary',
  '.btn-secondary',
  '.btn-ghost',
  '.btn-danger',
  '.btn-success',
  '.btn-warning',
  '.btn-sm',
  '.btn-md',
  '.btn-lg',
]

// FIELD_SELECTORS covers the shared field recipes that text inputs and dropdown
// triggers combine.
const FIELD_SELECTORS = ['.input', '.input-error', '.input-trigger']

// pointerFocusTokens returns utilities that match on any focus, including pointer
// focus, or that scale a control while pressed. focus-visible variants are excluded
// because browsers apply them only when the focus should be shown.
function pointerFocusTokens(tokens: string[]): string[] {
  return tokens.filter((token) => /(^|:)focus:/.test(token) || /(^|:)active:scale/.test(token))
}

// haloTokens returns ring utilities under any variant prefix. Shared controls draw
// focus and open states with borders or outlines, so any ring token is a halo.
function haloTokens(tokens: string[]): string[] {
  return tokens.filter((token) => /(^|:)ring(-|$)/.test(token))
}

describe('focus indication', () => {
  // The base button recipe draws focus only through the keyboard outline, and no
  // variant adds a focus ring or press scaling that a mouse click would trigger.
  it('limits button focus styling to the keyboard outline', () => {
    const style = postcss.parse(readSource('style.css'))
    expect(cssRules(style, '.btn').flatMap(applyTokens)).toEqual(
      expect.arrayContaining(['outline-none', ...KEYBOARD_OUTLINE]),
    )
    for (const selector of BUTTON_SELECTORS) {
      expect(pointerFocusTokens(cssRules(style, selector).flatMap(applyTokens))).toEqual([])
    }
  })

  // Text fields strengthen their border on focus-visible, which browsers match on
  // every text-field focus but only on keyboard focus for button triggers. Triggers
  // add the button outline for keyboard users, and no field recipe draws a halo.
  it('marks field focus with borders and trigger focus with the keyboard outline', () => {
    const style = postcss.parse(readSource('style.css'))
    expect(cssRules(style, '.input').flatMap(applyTokens)).toEqual(
      expect.arrayContaining([
        'outline-none',
        'focus-visible:border-black/30',
        'dark:focus-visible:border-dark-400',
      ]),
    )
    expect(cssRules(style, '.input-trigger').flatMap(applyTokens)).toEqual(
      expect.arrayContaining(KEYBOARD_OUTLINE),
    )
    for (const selector of FIELD_SELECTORS) {
      const tokens = cssRules(style, selector).flatMap(applyTokens)
      expect(pointerFocusTokens(tokens)).toEqual([])
      expect(haloTokens(tokens)).toEqual([])
    }
  })

  // Opening a dropdown keeps the trigger at its hover border. A ring here would
  // reappear as a halo every time the user clicks the trigger.
  it.each([
    ['components/common/Select.vue', '.select-trigger-open'],
    ['components/common/DateRangePicker.vue', '.date-picker-trigger-open'],
    ['components/common/DateTimePicker.vue', '.date-time-picker-trigger-open'],
    ['components/admin/proxy/ProxySelector.vue', '.select-trigger-open'],
  ])('%s marks the open trigger without a halo', (path, selector) => {
    const tokens = cssRules(parseVue(path).styles, selector).flatMap(applyTokens)
    expect(tokens).toEqual(expect.arrayContaining(['border-black/20', 'dark:border-dark-500']))
    expect(haloTokens(tokens)).toEqual([])
  })

  // Tag-input shells contain a text field and mark focus on the shell with
  // focus-within. Clicking into the field must strengthen the border only.
  it.each([
    'components/common/settings/SettingsTagInput.vue',
    'components/admin/pricing/ModelTagInput.vue',
    'views/admin/settings/AgreementSettings.vue',
  ])('%s marks shell focus without a halo', (path) => {
    const shells = parseVue(path).elements.filter((element) =>
      classes(element).some((token) => token.startsWith('focus-within:')),
    )
    expect(shells.length).toBeGreaterThan(0)
    for (const shell of shells) {
      expect(classes(shell)).toEqual(expect.arrayContaining(['focus-within:border-black/30']))
      expect(haloTokens(classes(shell))).toEqual([])
    }
  })

  // The switch track uses the same keyboard outline as buttons. A click toggles
  // the switch without leaving a ring around the track.
  it('limits switch focus styling to the keyboard outline', () => {
    const [track] = parseVue('components/common/Toggle.vue').elements
    expect(classes(track)).toEqual(expect.arrayContaining(['outline-none', ...KEYBOARD_OUTLINE]))
    expect(pointerFocusTokens(classes(track))).toEqual([])
  })

  // Save bars place buttons on a dark surface. The outline offset is transparent,
  // so these buttons need no surface-colored ring offset of their own.
  it.each([
    'views/admin/SettingsView.vue',
    'views/admin/plugins/detail/PluginConfigPanel.vue',
    'views/admin/plugins/detail/PluginLimitsPanel.vue',
  ])('%s save bar buttons add no pointer focus styling', (path) => {
    const buttons = parseVue(path).elements.filter((element) => classes(element).includes('btn'))
    expect(buttons.length).toBeGreaterThan(0)
    for (const button of buttons) {
      expect(pointerFocusTokens(classes(button))).toEqual([])
    }
  })
})
