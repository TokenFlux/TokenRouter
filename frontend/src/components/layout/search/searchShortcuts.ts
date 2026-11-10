// searchShortcuts.ts recognizes the keyboard shortcuts that open the global search palette
// and formats their labels for the current platform. Apple keyboards use Command where
// other systems use Control, so both the trigger hint and the palette footer read the
// modifier from here instead of assuming one platform.

// NavigatorWithUAData adds the optional User-Agent Client Hints platform field, which
// Chromium browsers expose and Safari and Firefox omit.
interface NavigatorWithUAData extends Navigator {
  userAgentData?: { platform?: string } // Client Hints platform.
}

// isApplePlatform reports whether the browser runs on macOS or iOS. It prefers Client
// Hints and falls back to the deprecated navigator.platform, which every browser still
// provides; without a navigator it returns false.
export function isApplePlatform(): boolean {
  if (typeof navigator === 'undefined') return false
  const platform =
    (navigator as NavigatorWithUAData).userAgentData?.platform ?? navigator.platform ?? ''
  return /mac|iphone|ipad|ipod/i.test(platform)
}

// modifierLabel returns the symbol or name of the platform's primary shortcut modifier.
export function modifierLabel(): string {
  return isApplePlatform() ? '⌘' : 'Ctrl'
}

// hasPrimaryModifier reports whether the platform's primary modifier is held: Command on
// Apple platforms and Control elsewhere. Checking only that key leaves Control+K on macOS
// to text fields, where it deletes to the end of the line.
export function hasPrimaryModifier(event: KeyboardEvent): boolean {
  return isApplePlatform() ? event.metaKey : event.ctrlKey
}

// isToggleShortcut reports whether the event is the palette's open and close shortcut,
// Command+K or Control+K with no other modifiers.
export function isToggleShortcut(event: KeyboardEvent): boolean {
  return (
    hasPrimaryModifier(event) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k'
  )
}

// isEditableTarget reports whether keyboard input at the target belongs to a text field,
// a select, or editable content, where "/" must type a character instead of opening
// the palette.
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  return (
    target.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"]') !==
    null
  )
}

// isSlashShortcut reports whether the event is an unmodified "/" typed outside editable
// content. Input method composition is excluded because it can report "/" while the
// user is still composing text.
export function isSlashShortcut(event: KeyboardEvent): boolean {
  return (
    event.key === '/' &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.altKey &&
    !event.isComposing &&
    !isEditableTarget(event.target)
  )
}
