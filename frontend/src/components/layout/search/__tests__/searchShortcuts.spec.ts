import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  isEditableTarget,
  isSlashShortcut,
  isToggleShortcut,
  modifierLabel,
} from '../searchShortcuts'

// searchShortcuts.spec.ts checks the keyboard shortcuts that open the global search
// palette on Apple and other platforms. The platform is switched by stubbing
// navigator.platform, which jsdom exposes without Client Hints.

// setPlatform makes the shortcut helpers see the given navigator platform string.
function setPlatform(platform: string) {
  vi.spyOn(navigator, 'platform', 'get').mockReturnValue(platform)
}

afterEach(() => {
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('toggle shortcut', () => {
  // Apple platforms use Command+K and leave Control+K to text fields; other platforms use
  // Control+K. Extra modifiers never trigger the palette.
  it('uses the platform modifier', () => {
    setPlatform('MacIntel')
    expect(modifierLabel()).toBe('⌘')
    expect(isToggleShortcut(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))).toBe(true)
    expect(isToggleShortcut(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))).toBe(false)

    setPlatform('Win32')
    expect(modifierLabel()).toBe('Ctrl')
    expect(isToggleShortcut(new KeyboardEvent('keydown', { key: 'K', ctrlKey: true }))).toBe(true)
    expect(
      isToggleShortcut(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, shiftKey: true })),
    ).toBe(false)
  })
})

describe('slash shortcut', () => {
  // "/" opens the palette from the page but types a character in inputs, text areas, and
  // editable content, including elements nested inside an editable region.
  it('ignores editable targets', () => {
    document.body.innerHTML =
      '<input id="field" /><div contenteditable="true"><span id="nested">x</span></div><p id="text">x</p>'
    const slashAt = (id: string) => {
      const event = new KeyboardEvent('keydown', { key: '/', bubbles: true })
      Object.defineProperty(event, 'target', { value: document.getElementById(id) })
      return event
    }
    expect(isEditableTarget(document.getElementById('field'))).toBe(true)
    expect(isSlashShortcut(slashAt('field'))).toBe(false)
    expect(isSlashShortcut(slashAt('nested'))).toBe(false)
    expect(isSlashShortcut(slashAt('text'))).toBe(true)
  })
})
