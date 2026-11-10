import { computed, readonly, ref } from 'vue'

// globalSearchState.ts holds the shared state of the global search palette. The palette
// is mounted once by App.vue, while its triggers live in different headers (AppHeader and
// the guest marketplace header), so they share this module-level state instead of passing
// events through the layout tree.
//
// The palette listens for keyboard shortcuts only while at least one trigger is mounted.
// Pages without a visible search box, such as login or setup, therefore keep "/" and
// Command+K for their own use. Triggers register on mount and release on unmount; the
// count tolerates overlapping mounts during route transitions, when the leaving and
// entering headers can both exist briefly.

// open records whether the palette is visible; only the functions below change it.
const open = ref(false)

// mountedTriggers counts mounted search triggers that enable the keyboard shortcuts.
const mountedTriggers = ref(0)

// globalSearchOpen exposes the palette visibility to the palette and its triggers.
export const globalSearchOpen = readonly(open)

// globalSearchAvailable reports whether a search trigger is on screen, which enables the
// palette's keyboard shortcuts.
export const globalSearchAvailable = computed(() => mountedTriggers.value > 0)

// openGlobalSearch shows the palette; calling it while open has no effect.
export function openGlobalSearch(): void {
  open.value = true
}

// closeGlobalSearch hides the palette; calling it while closed has no effect.
export function closeGlobalSearch(): void {
  open.value = false
}

// registerSearchTrigger records a mounted trigger and returns the matching release
// function. Call the release exactly once when the trigger unmounts; extra calls are
// ignored so the count cannot drop below the triggers still on screen.
export function registerSearchTrigger(): () => void {
  mountedTriggers.value++
  let released = false
  return () => {
    if (released) return
    released = true
    mountedTriggers.value--
  }
}
