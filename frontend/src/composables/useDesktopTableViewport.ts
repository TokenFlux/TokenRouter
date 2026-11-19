import { onMounted, onUnmounted, readonly, ref, type Ref } from 'vue'
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'

/**
 * useDesktopTableViewport.ts shares a single reference-counted matchMedia
 * subscription across all mounted table rows so large provider or usage pages
 * observe desktop-versus-mobile viewport transitions without registering one
 * browser MediaQueryList listener per row.
 */

/**
 * canUseMatchMedia reports whether the current runtime exposes window.matchMedia.
 */
const canUseMatchMedia = () =>
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'

/**
 * readDesktopMatches evaluates the shared desktop breakpoint or defaults to true
 * in non-browser test environments without matchMedia.
 */
const readDesktopMatches = (): boolean =>
  canUseMatchMedia() ? window.matchMedia(TABLE_DESKTOP_MEDIA_QUERY).matches : true

/**
 * Shared singleton state tracks active composable subscribers and detaches the
 * underlying MediaQueryList listener when the last subscriber unmounts.
 */
const sharedIsDesktop = ref(true)
let activeSubscribers = 0
let sharedMediaQuery: MediaQueryList | null = null
let sharedMatchMediaFn: typeof window.matchMedia | null = null
let sharedListener: ((event: MediaQueryListEvent) => void) | null = null

/**
 * releaseMediaQueryListener removes the active browser listener from
 * sharedMediaQuery and clears the singleton handles.
 */
function releaseMediaQueryListener() {
  if (!sharedMediaQuery || !sharedListener) return
  if (typeof sharedMediaQuery.removeEventListener === 'function') {
    sharedMediaQuery.removeEventListener('change', sharedListener)
  } else {
    sharedMediaQuery.removeListener(sharedListener)
  }
  sharedListener = null
  sharedMediaQuery = null
  sharedMatchMediaFn = null
}

/**
 * attachSharedViewportListener initializes the singleton MediaQueryList listener
 * when the first subscriber mounts and refreshes the reactive match state.
 */
function attachSharedViewportListener() {
  sharedIsDesktop.value = readDesktopMatches()
  if (!canUseMatchMedia()) return
  activeSubscribers++
  if (sharedMediaQuery && sharedMatchMediaFn === window.matchMedia) return
  if (sharedMediaQuery) {
    releaseMediaQueryListener()
  }

  sharedMatchMediaFn = window.matchMedia
  sharedMediaQuery = window.matchMedia(TABLE_DESKTOP_MEDIA_QUERY)
  sharedIsDesktop.value = sharedMediaQuery.matches
  sharedListener = (event: MediaQueryListEvent) => {
    sharedIsDesktop.value = event.matches
  }
  if (typeof sharedMediaQuery.addEventListener === 'function') {
    sharedMediaQuery.addEventListener('change', sharedListener)
  } else {
    sharedMediaQuery.addListener(sharedListener)
  }
}

/**
 * detachSharedViewportListener decrements the subscriber count and removes the
 * browser MediaQueryList listener when no mounted subscribers remain.
 */
function detachSharedViewportListener() {
  if (activeSubscribers > 0) {
    activeSubscribers--
  }
  if (activeSubscribers > 0) return
  releaseMediaQueryListener()
}

/**
 * useDesktopTableViewport returns a reactive readonly boolean reflecting
 * TABLE_DESKTOP_MEDIA_QUERY, backed by one shared browser listener across all
 * mounted consumers.
 */
export function useDesktopTableViewport(): Readonly<Ref<boolean>> {
  sharedIsDesktop.value = readDesktopMatches()
  let attached = false

  onMounted(() => {
    attached = true
    attachSharedViewportListener()
  })

  onUnmounted(() => {
    if (!attached) return
    attached = false
    detachSharedViewportListener()
  })

  return readonly(sharedIsDesktop)
}
