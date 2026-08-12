<template>
  <div
    class="pointer-events-none absolute inset-0 z-0 select-none overflow-hidden transition-opacity duration-700"
    :class="loaded ? 'opacity-100' : 'opacity-0'"
    aria-hidden="true"
  >
    <div
      ref="container"
      class="absolute left-1/2 top-1/2 h-[2160px] w-[3840px] -translate-x-1/2 -translate-y-1/2"
    ></div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { AnimationItem, LottiePlayer } from 'lottie-web'
import { useTheme } from '@/composables/useTheme'

const { isDark } = useTheme()

const container = ref<HTMLDivElement>()
const loaded = ref(false)

let player: LottiePlayer | null = null
let animation: AnimationItem | null = null
let loadToken = 0

const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches

async function loadAnimation(dark: boolean) {
  const token = ++loadToken

  const [lottieModule, dataModule] = await Promise.all([
    player ? Promise.resolve(null) : import('lottie-web'),
    dark
      ? import('@/assets/lottie/auth-bg-dark.json')
      : import('@/assets/lottie/auth-bg-light.json'),
  ])

  // 主题在加载期间又被切换，或组件已卸载时放弃本次结果
  if (token !== loadToken || !container.value) return

  if (lottieModule) {
    player = lottieModule.default
  }

  animation?.destroy()
  animation = player!.loadAnimation({
    container: container.value,
    renderer: 'svg',
    loop: true,
    autoplay: !prefersReducedMotion,
    animationData: dataModule.default,
    rendererSettings: { preserveAspectRatio: 'xMidYMid slice' },
  })

  if (prefersReducedMotion) {
    animation.goToAndStop(0, true)
  }

  loaded.value = true
}

onMounted(() => {
  loadAnimation(isDark.value)
})

watch(isDark, (dark) => {
  loadAnimation(dark)
})

onBeforeUnmount(() => {
  loadToken++
  animation?.destroy()
  animation = null
})
</script>
