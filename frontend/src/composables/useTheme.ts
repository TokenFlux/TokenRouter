import { ref } from 'vue'

type ViewTransition = {
  ready: Promise<void>
  finished: Promise<void>
}

type ViewTransitionDocument = {
  startViewTransition?: (callback: () => void) => ViewTransition
}

const themeStorageKey = 'theme'
const isDark = ref(document.documentElement.classList.contains('dark'))
const themeTransitionClass = 'theme-transitioning'
const themeRippleXVariable = '--theme-ripple-x'
const themeRippleYVariable = '--theme-ripple-y'
const themeRippleRadiusVariable = '--theme-ripple-radius'

function prefersDarkMode(): boolean {
  const savedTheme = localStorage.getItem(themeStorageKey)
  return savedTheme === 'dark' || (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
}

function applyTheme(nextIsDark: boolean) {
  isDark.value = nextIsDark
  document.documentElement.classList.toggle('dark', nextIsDark)
}

export function initTheme() {
  applyTheme(prefersDarkMode())
}

function persistTheme(nextIsDark: boolean) {
  applyTheme(nextIsDark)
  localStorage.setItem(themeStorageKey, nextIsDark ? 'dark' : 'light')
}

function supportsAnimatedTheme(event?: MouseEvent): event is MouseEvent {
  const viewTransitionDocument = document as unknown as ViewTransitionDocument
  return Boolean(
    event &&
      viewTransitionDocument.startViewTransition &&
      !window.matchMedia('(prefers-reduced-motion: reduce)').matches
  )
}

function getRippleRadius(x: number, y: number): number {
  return Math.hypot(
    Math.max(x, window.innerWidth - x),
    Math.max(y, window.innerHeight - y)
  )
}

function prepareThemeRipple(x: number, y: number) {
  const root = document.documentElement
  root.style.setProperty(themeRippleXVariable, `${x}px`)
  root.style.setProperty(themeRippleYVariable, `${y}px`)
  root.style.setProperty(themeRippleRadiusVariable, `${getRippleRadius(x, y)}px`)
  root.classList.add(themeTransitionClass)
}

function cleanupThemeRipple() {
  const root = document.documentElement
  root.classList.remove(themeTransitionClass)
  root.style.removeProperty(themeRippleXVariable)
  root.style.removeProperty(themeRippleYVariable)
  root.style.removeProperty(themeRippleRadiusVariable)
}

export function setTheme(nextIsDark: boolean, event?: MouseEvent) {
  if (nextIsDark === isDark.value) return

  if (!supportsAnimatedTheme(event)) {
    persistTheme(nextIsDark)
    return
  }

  const { clientX, clientY } = event
  prepareThemeRipple(clientX, clientY)

  const viewTransitionDocument = document as unknown as ViewTransitionDocument
  const transition = viewTransitionDocument.startViewTransition?.(() => {
    persistTheme(nextIsDark)
  })

  if (transition) {
    void transition.finished.finally(cleanupThemeRipple)
  } else {
    cleanupThemeRipple()
    persistTheme(nextIsDark)
  }
}

export function useTheme() {
  return {
    isDark,
    setTheme,
    toggleTheme: (event?: MouseEvent) => setTheme(!isDark.value, event),
  }
}
