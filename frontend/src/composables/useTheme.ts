import { ref } from 'vue'

type ViewTransition = {
  ready: Promise<void>
}

type ViewTransitionDocument = {
  startViewTransition?: (callback: () => void) => ViewTransition
}

const themeStorageKey = 'theme'
const themeRippleDuration = 640
const isDark = ref(document.documentElement.classList.contains('dark'))

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

function animateThemeRipple(transition: ViewTransition, x: number, y: number) {
  void transition.ready
    .then(() => {
      const endRadius = getRippleRadius(x, y)
      const options: KeyframeAnimationOptions & { pseudoElement: string } = {
        duration: themeRippleDuration,
        easing: 'cubic-bezier(0.65, 0, 0.35, 1)',
        pseudoElement: '::view-transition-new(root)',
      }

      // 用新主题截图做圆形裁剪，让颜色从点击位置像水波一样扩散。
      document.documentElement.animate(
        {
          clipPath: [
            `circle(0px at ${x}px ${y}px)`,
            `circle(${endRadius}px at ${x}px ${y}px)`,
          ],
        },
        options
      )
    })
    .catch(() => undefined)
}

export function setTheme(nextIsDark: boolean, event?: MouseEvent) {
  if (nextIsDark === isDark.value) return

  if (!supportsAnimatedTheme(event)) {
    persistTheme(nextIsDark)
    return
  }

  const { clientX, clientY } = event
  const viewTransitionDocument = document as unknown as ViewTransitionDocument
  const transition = viewTransitionDocument.startViewTransition?.(() => {
    persistTheme(nextIsDark)
  })

  if (transition) {
    animateThemeRipple(transition, clientX, clientY)
  } else {
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
