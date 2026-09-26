import { ref } from 'vue'

const themeStorageKey = 'theme'
const themeSwitchingClass = 'theme-switching'
const isDark = ref(document.documentElement.classList.contains('dark')) // check-ui-allow: 响应式主题原语本身
let themeSwitchFrame: number | undefined

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

function suspendThemeTransitions() {
  const root = document.documentElement
  root.classList.add(themeSwitchingClass)

  if (themeSwitchFrame !== undefined) {
    window.cancelAnimationFrame(themeSwitchFrame)
  }

  // 保留两个渲染帧，确保 Vue 更新和浏览器绘制期间都不会触发零散的颜色过渡。
  themeSwitchFrame = window.requestAnimationFrame(() => {
    themeSwitchFrame = window.requestAnimationFrame(() => {
      root.classList.remove(themeSwitchingClass)
      themeSwitchFrame = undefined
    })
  })
}

export function setTheme(nextIsDark: boolean) {
  if (nextIsDark === isDark.value) return
  suspendThemeTransitions()
  // 整体直接切换主题，避免不同组件按各自时长产生拖尾。
  persistTheme(nextIsDark)
}

export function useTheme() {
  return {
    isDark,
    setTheme,
    toggleTheme: () => setTheme(!isDark.value),
  }
}
