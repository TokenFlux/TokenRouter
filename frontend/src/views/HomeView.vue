<template>
  <div v-if="homeContent" class="min-h-screen">
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <div v-else v-html="homeContent"></div>
  </div>

  <div v-else class="tf-site ba-theme-shell" :style="heroMotionVars" @pointermove="handlePointerMove">
    <div class="tf-backdrop ba-theme-backdrop" aria-hidden="true"></div>

    <header class="tf-header">
      <nav class="tf-nav" aria-label="Home navigation">
        <router-link to="/home" class="tf-brand" :aria-label="siteName">
          <span class="tf-brand-mark">
            <img :src="siteLogo || '/logo.png'" alt="Logo" />
          </span>
          <span class="tf-brand-copy">
            <strong>{{ siteName }}</strong>
            <small>{{ copy.brandSubtitle }}</small>
          </span>
        </router-link>

        <div class="tf-nav-actions">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="tf-icon-link"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            type="button"
            class="tf-icon-link"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link v-if="isAuthenticated" :to="dashboardPath" class="tf-login tf-login-user">
            <span>{{ userInitial }}</span>
            {{ t('home.dashboard') }}
          </router-link>
          <router-link v-else to="/login" class="tf-login">
            {{ t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="tf-main">
      <section class="tf-hero">
        <div class="tf-hero-visual" aria-hidden="true">
        </div>

        <div class="tf-hero-content">
          <p class="tf-kicker">{{ copy.heroBadge }}</p>
          <h1 class="tf-hero-title">{{ copy.heroTitle }}</h1>
          <p class="tf-hero-lead">{{ copy.heroLead }}</p>
          <p class="tf-hero-sub">{{ copy.heroSub }}</p>

          <div class="tf-hero-actions">
            <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="tf-primary">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="md" />
            </router-link>
            <router-link to="/models" class="tf-secondary">
              {{ t('home.exploreMarketplace') }}
            </router-link>
            <a href="#about" class="tf-secondary tf-secondary-soft">
              {{ copy.aboutNav }}
            </a>
          </div>
        </div>
      </section>

      <a href="#about" class="tf-swipe-cue" aria-label="Scroll to about">
        <span>{{ copy.swipeCue }}</span>
        <Icon name="arrowRight" size="md" />
      </a>

      <section id="about" class="tf-story">
        <div class="tf-section-heading">
          <p>{{ copy.storyEyebrow }}</p>
          <h2>{{ copy.storyTitle }}</h2>
        </div>

        <div class="tf-about-panel">
          <div class="tf-about-copy">
            <p v-for="item in copy.aboutParagraphs" :key="item">{{ item }}</p>
          </div>
          <div class="tf-growth-panel">
            <div class="tf-growth-top">
              <span>{{ copy.growthEyebrow }}</span>
              <strong>{{ copy.growthTitle }}</strong>
            </div>

            <div class="tf-growth-stats">
              <div class="tf-growth-stat tf-growth-clock">
                <span>{{ copy.stableRunLabel }}</span>
                <strong>{{ stableRunDuration.days }} {{ copy.dayUnit }}</strong>
                <div class="tf-clock-ticks" aria-label="stable run timer">
                  <span>{{ stableRunDuration.hours }}</span>
                  <span>{{ stableRunDuration.minutes }}</span>
                  <span>{{ stableRunDuration.seconds }}</span>
                </div>
              </div>
              <div class="tf-growth-stat">
                <span>{{ copy.dailyTokenLabel }}</span>
                <strong>{{ copy.dailyTokenValue }}</strong>
                <small>{{ copy.dailyTokenCaption }}</small>
              </div>
            </div>

            <div class="tf-growth-chart" aria-hidden="true">
              <div class="tf-chart-bars">
                <span
                  v-for="(bar, index) in copy.growthBars"
                  :key="`${bar}-${index}`"
                  :style="{ height: `${bar}%` }"
                ></span>
              </div>
              <svg viewBox="0 0 240 90" role="img">
                <polyline points="8,74 44,68 82,54 122,42 166,30 206,22 232,12" />
              </svg>
            </div>

            <p>{{ copy.growthCaption }}</p>
          </div>
        </div>

        <div class="tf-story-grid">
          <article v-for="item in copy.storyCards" :key="item.title" class="tf-story-card">
            <span>{{ item.icon }}</span>
            <h3>{{ item.title }}</h3>
            <p>{{ item.body }}</p>
          </article>
        </div>
      </section>

      <section class="tf-scroll-stage">
        <div class="tf-section-heading tf-stage-heading">
          <p>{{ copy.workflowEyebrow }}</p>
          <h2>{{ copy.workflowTitle }}</h2>
        </div>

        <div class="tf-stage-grid">
          <div class="tf-stage-copy">
            <article v-for="step in copy.workflow" :key="step.title" class="tf-step">
              <span>{{ step.index }}</span>
              <h3>{{ step.title }}</h3>
              <p>{{ step.body }}</p>
            </article>
          </div>

          <div class="tf-code-stage">
            <div class="tf-code-title">
              <span></span>
              <span></span>
              <span></span>
              terminal
            </div>
            <pre><code>{{ copy.code }}</code></pre>
            <div class="tf-code-tags">
              <span v-for="item in copy.workflowLogos" :key="item.name">
                <img :src="item.logo" :alt="`${item.name} logo`" loading="lazy" />
                {{ item.name }}
              </span>
            </div>
          </div>
        </div>
      </section>

      <section class="tf-models">
        <div class="tf-section-heading">
          <p>{{ copy.modelsEyebrow }}</p>
          <h2>{{ copy.modelsTitle }}</h2>
        </div>

        <div class="tf-model-grid">
          <article v-for="model in copy.modelGroups" :key="model.name" class="tf-model-card">
            <div class="tf-model-card-top">
              <div>
                <p>{{ model.platform }}</p>
                <h3>{{ model.name }}</h3>
              </div>
              <span class="tf-model-logo">
                <img :src="model.logo" :alt="`${model.name} logo`" loading="lazy" />
              </span>
            </div>
            <strong>{{ model.count }}</strong>
            <span>{{ model.examples }}</span>
          </article>
        </div>
        <p class="tf-model-note">{{ copy.modelNote }}</p>
      </section>

      <section class="tf-culture">
        <div class="tf-culture-mark">🍥</div>
        <div>
          <p>{{ copy.cultureEyebrow }}</p>
          <h2>{{ copy.cultureTitle }}</h2>
          <p>{{ copy.cultureBody }}</p>
        </div>
      </section>
    </main>

    <footer class="tf-footer">
      <span>&copy; {{ currentYear }} {{ siteName }}</span>
      <div>
        <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">{{ t('home.docs') }}</a>
        <a :href="githubUrl" target="_blank" rel="noopener noreferrer">GitHub</a>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { useTheme } from '@/composables/useTheme'

const { t, locale } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || 'https://docs.tokenflux.dev')
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')

const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

const { isDark, toggleTheme } = useTheme()
const githubUrl = 'https://github.com/TokenFlux/TokenRouter'

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
const userInitial = computed(() => {
  const user = authStore.user
  if (!user?.email) return ''
  return user.email.charAt(0).toUpperCase()
})
const currentYear = computed(() => new Date().getFullYear())
const stableRunStart = new Date('2026-04-06T00:00:00+08:00').getTime()
const now = ref(Date.now())
const scrollProgress = ref(0)
const pointerX = ref(50)
const pointerY = ref(50)
let stableRunTimer: number | undefined
let scrollRaf = 0

const padDuration = (value: number) => value.toString().padStart(2, '0')
const stableRunDuration = computed(() => {
  const diff = Math.max(0, now.value - stableRunStart)
  const totalSeconds = Math.floor(diff / 1000)
  const days = Math.floor(totalSeconds / 86400)
  const hours = Math.floor((totalSeconds % 86400) / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60

  return {
    days,
    hours: padDuration(hours),
    minutes: padDuration(minutes),
    seconds: padDuration(seconds),
  }
})

const clamp = (value: number, min = 0, max = 1) => Math.min(max, Math.max(min, value))
const phase = (start: number, end: number) => clamp((scrollProgress.value - start) / (end - start))
const easeOutCubic = (value: number) => 1 - Math.pow(1 - value, 3)
const easeJelly = (value: number) => {
  const c1 = 1.70158
  const c3 = c1 + 1
  return 1 + c3 * Math.pow(value - 1, 3) + c1 * Math.pow(value - 1, 2)
}

const updateScrollProgress = () => {
  scrollRaf = 0
  scrollProgress.value = clamp(window.scrollY / 360)
}

const requestScrollProgress = () => {
  if (!scrollRaf) {
    scrollRaf = window.requestAnimationFrame(updateScrollProgress)
  }
}

const handlePointerMove = (event: PointerEvent) => {
  const target = event.currentTarget as HTMLElement | null
  const rect = target?.getBoundingClientRect()
  if (!rect) return
  pointerX.value = clamp(((event.clientX - rect.left) / rect.width) * 100, 0, 100)
  pointerY.value = clamp(((event.clientY - rect.top) / rect.height) * 100, 0, 100)
}

const heroMotionVars = computed(() => {
  const p = scrollProgress.value
  const trigger = easeOutCubic(phase(0, 0.22))
  const textGone = easeOutCubic(phase(0.04, 0.28))
  const squish = easeJelly(phase(0.22, 0.56))
  const collect = easeOutCubic(phase(0.56, 0.88))
  const settle = easeJelly(phase(0.78, 1))
  const nav = easeJelly(phase(0.74, 1))
  const titleExit = easeOutCubic(phase(0.12, 0.42))
  const ball = Math.sin(phase(0.18, 0.72) * Math.PI)
  const titleScaleY = Math.max(0.76, 1 - 0.08 * squish - 0.16 * titleExit)
  const titleScaleX = Math.max(0.9, 1 + 0.055 * squish - 0.09 * titleExit)
  const membrane = Math.sin(clamp(p * 1.2) * Math.PI)
  const drop = Math.sin(clamp((p - 0.18) / 0.58) * Math.PI)

  return {
    '--tf-hero-y': `${-10 * trigger - 34 * collect + Math.sin(settle * Math.PI) * 5}px`,
    '--tf-hero-bg-scale-y': `${Math.max(0.095, 1 - 0.86 * ball - 0.82 * collect + membrane * 0.035)}`,
    '--tf-hero-bg-scale-x': `${Math.max(0.13, 1 - 0.86 * ball - 0.02 * collect + membrane * 0.035)}`,
    '--tf-hero-bg-radius': `${2 + ball * 18 - collect * 0.75}rem`,
    '--tf-hero-bg-blur': `${14 * collect}px`,
    '--tf-hero-bg-jelly': `${membrane * 34}px`,
    '--tf-hero-shell-y': `${-16 * trigger - 74 * collect + Math.sin(settle * Math.PI) * 8}px`,
    '--tf-hero-shell-shadow': `${0.12 + collect * 0.16}`,
    '--tf-liquid-opacity': `${clamp(membrane * 1.35 - collect * 0.2)}`,
    '--tf-liquid-lip-y': `${-16 * trigger - 180 * collect + membrane * 28}px`,
    '--tf-liquid-lip-scale-x': `${1 + membrane * 0.55 - collect * 0.35}`,
    '--tf-liquid-lip-scale-y': `${0.75 + membrane * 1.1 - collect * 0.62}`,
    '--tf-liquid-drop-x': `${-28 + collect * 44}vw`,
    '--tf-liquid-drop-y': `${28 - collect * 104 + drop * 22}px`,
    '--tf-liquid-drop-scale': `${0.32 + drop * 0.9 - collect * 0.34}`,
    '--tf-line-scale-x': `${1 + membrane * 0.07}`,
    '--tf-hero-title-scale-x': `${titleScaleX}`,
    '--tf-hero-title-scale-y': `${titleScaleY}`,
    '--tf-hero-title-x': `${-10 * titleExit}vw`,
    '--tf-hero-title-y': `${-18 * titleExit - membrane * 3}px`,
    '--tf-hero-title-opacity': `${Math.max(0, 1 - titleExit * 1.5)}`,
    '--tf-hero-lead-opacity': `${Math.max(0, 1 - textGone * 1.25)}`,
    '--tf-hero-lead-y': `${-24 * textGone}px`,
    '--tf-hero-sub-opacity': `${Math.max(0, 1 - trigger * 2.4)}`,
    '--tf-hero-sub-y': `${-28 * trigger}px`,
    '--tf-hero-badge-opacity': `${Math.max(0, 1 - phase(0.12, 0.34) * 1.4)}`,
    '--tf-hero-badge-y': `${-16 * phase(0.12, 0.34)}px`,
    '--tf-hero-actions-y': `${-18 * squish - 72 * collect}px`,
    '--tf-hero-actions-scale': `${Math.max(0.62, 1 - 0.08 * squish - 0.3 * collect)}`,
    '--tf-hero-actions-gap': `${Math.max(0.22, 0.8 - 0.58 * (squish + collect) / 2)}rem`,
    '--tf-hero-actions-opacity': `${Math.max(0, 1 - collect * 0.78)}`,
    '--tf-nav-y': `${-112 + 112 * nav + Math.sin(nav * Math.PI) * -4}%`,
    '--tf-nav-opacity': `${clamp(nav)}`,
    '--tf-nav-blur': `${8 + 14 * nav}px`,
    '--tf-cue-opacity': `${Math.max(0, 1 - p * 1.8)}`,
    '--tf-cue-y': `${p * 22}px`,
    '--tf-backdrop-tilt': `${p * 7}deg`,
    '--tf-backdrop-y': `${-18 * p}px`,
    '--tf-pointer-x': `${pointerX.value}%`,
    '--tf-pointer-y': `${pointerY.value}%`,
    '--tf-title-flow-x': `${pointerX.value}%`,
    '--tf-title-flow-y': `${pointerY.value}%`,
  } as Record<string, string>
})

const iconBase =
  'https://raw.githubusercontent.com/lobehub/lobe-icons/refs/heads/master/packages/static-png/light'

const zhHomeCopy = {
  brandSubtitle: '词元流动',
  aboutNav: '关于',
  swipeCue: '向上滑动',
  heroBadge: '🍥 一个半公益中转站，永不跑路',
  heroTitle: '词元流动',
  heroLead: '一个 API Key，接入所有平台',
  heroSub: '用爱发电，我们共同的小社区。适合在 Codex、Claude Code、Cherry Studio 等工具里直接使用。',
  storyEyebrow: '关于',
  storyTitle: '从一个自用中转站开始',
  aboutParagraphs: [
    '一开始，它只是一个学生为了降低自己和朋友的 token 成本搭起来的中转站。',
    '试开放注册第一天来了 40 个用户，7 天达到 125 个用户和 67,563 个请求。人数变多之后，最先补的不是包装，而是可用性、异常兜底和用量监控。',
    '后面会继续把稳定性做扎实，也会接入更多模型。但最初那个简单的问题不变：能不能有一个更便宜、更透明、用起来更省心的入口。',
  ],
  growthEyebrow: '🍥 运行状态',
  growthTitle: '稳定增长中',
  stableRunLabel: '稳定运行',
  dayUnit: '天',
  dailyTokenLabel: '每日消耗',
  dailyTokenValue: '250B+',
  dailyTokenCaption: 'tokens / day',
  growthBars: [18, 26, 38, 48, 61, 76, 92],
  growthCaption: '用户增长数据先收起来，图表保留；现在更关注稳定运行和实际 token 规模。',
  storyCards: [
    {
      icon: '01',
      title: '半公益',
      body: '先解决自己和朋友的问题，再把入口开放给更多有同样需求的人。',
    },
    {
      icon: '02',
      title: '先把稳定做好',
      body: '扩容、监控、异常兜底会继续补上；便宜之外，稳定才是长期使用的底线。',
    },
    {
      icon: '🍥',
      title: '更多模型接入中',
      body: '在保证可用性的前提下，继续接入更多模型、分组和工具。',
    },
  ],
  workflowEyebrow: '接入方式',
  workflowTitle: '用你熟悉的工具接入',
  workflowLogos: [
    { name: 'OpenAI API', logo: `${iconBase}/openai.png` },
    { name: 'Claude Code', logo: `${iconBase}/claude-color.png` },
    { name: 'Grok', logo: `${iconBase}/grok.png` },
    { name: 'DeepSeek', logo: `${iconBase}/deepseek-color.png` },
  ],
  workflow: [
    {
      index: 'A',
      title: '创建一个 API Key',
      body: '跟随文档生成密钥后，在 Agent 和 ChatBot 工具里复用。',
    },
    {
      index: 'B',
      title: '选择协议入口',
      body: '多数客户端用 OpenAI 兼容 /v1，Claude Code 等工作流用 Anthropic 端点。',
    },
    {
      index: 'C',
      title: '进入模型广场',
      body: '按任务在 ChatGPT Plus / Pro、Claude、Grok、DeepSeek 等分组之间切换。',
    },
  ],
  code: `curl https://tokenflux.dev/v1/chat/completions \\
  -H "Authorization: Bearer $TOKENFLUX_KEY" \\
  -d '{
    "model": "GPT-5.3 Codex",
    "messages": [{ "role": "user", "content": "Build with me." }]
  }'`,
  modelsEyebrow: '模型广场',
  modelsTitle: '模型广场，先看清再使用',
  modelNote: '更多模型和分组正在加入，模型广场会持续更新。',
  modelGroups: [
    {
      platform: 'OpenAI 兼容',
      name: 'ChatGPT Plus / Pro',
      count: '15 个模型',
      examples: 'codex-auto-review / GPT-5.2 / GPT-5.3 Codex',
      logo: `${iconBase}/openai.png`,
    },
    {
      platform: 'Anthropic 端点',
      name: 'Claude',
      count: '3 个模型',
      examples: 'Claude Code / Sonnet',
      logo: `${iconBase}/claude-color.png`,
    },
    {
      platform: 'OpenAI 兼容',
      name: 'Grok',
      count: '1 个模型',
      examples: 'grok-4.20 reasoning',
      logo: `${iconBase}/grok.png`,
    },
    {
      platform: 'Anthropic 端点',
      name: 'DeepSeek',
      count: '2 个模型',
      examples: 'DeepSeek V4，更多模型正在加入',
      logo: `${iconBase}/deepseek-color.png`,
    },
  ],
  cultureEyebrow: '初心',
  cultureTitle: '让 token 流动起来',
  cultureBody: 'token 太贵，所以想做一个更便宜、透明一点的入口。就这么简单。',
}

const enHomeCopy = {
  brandSubtitle: 'Token Flow',
  aboutNav: 'About',
  swipeCue: 'Swipe up',
  heroBadge: '🍥 A semi-public relay that stays around',
  heroTitle: 'Token Flow',
  heroLead: 'One API key for every platform.',
  heroSub: 'Powered by care, shaped by our small shared community. Ready for Codex, Claude Code, Cherry Studio, and common clients.',
  storyEyebrow: 'About',
  storyTitle: 'Starting from a small personal relay',
  aboutParagraphs: [
    'It began as a student-built relay to lower token costs for personal use and friends.',
    'The first open registration day brought 40 users. Within 7 days it reached 125 users and 67,563 requests. As more people joined, the first priority became availability, fallbacks, and usage visibility.',
    'The gateway will keep getting more stable and will keep adding models, while staying close to the original question: can access be cheaper, clearer, and easier to rely on?',
  ],
  growthEyebrow: '🍥 Live status',
  growthTitle: 'Stable growth',
  stableRunLabel: 'Stable run',
  dayUnit: 'days',
  dailyTokenLabel: 'Daily usage',
  dailyTokenValue: '250B+',
  dailyTokenCaption: 'tokens / day',
  growthBars: [18, 26, 38, 48, 61, 76, 92],
  growthCaption: 'User counts stay tucked away for now; the chart remains, with stability and token scale up front.',
  storyCards: [
    { icon: '01', title: 'Semi-public benefit', body: 'Solve a real problem first, then open the gateway to more people.' },
    { icon: '02', title: 'Stability first', body: 'Capacity, monitoring, and fallbacks keep improving; low cost only matters when the service is dependable.' },
    { icon: '🍥', title: 'More models joining', body: 'More models, groups, and tools will be added while keeping availability in view.' },
  ],
  workflowEyebrow: 'Access',
  workflowTitle: 'Use the tools you already know',
  workflowLogos: [
    { name: 'OpenAI API', logo: `${iconBase}/openai.png` },
    { name: 'Claude Code', logo: `${iconBase}/claude-color.png` },
    { name: 'Grok', logo: `${iconBase}/grok.png` },
    { name: 'DeepSeek', logo: `${iconBase}/deepseek-color.png` },
  ],
  workflow: [
    { index: 'A', title: 'Create one API key', body: 'Generate a key from the docs, then reuse it across Agent and ChatBot tools.' },
    { index: 'B', title: 'Pick an endpoint', body: 'Use OpenAI-compatible /v1 for most clients, or the Anthropic endpoint for Claude Code workflows.' },
    { index: 'C', title: 'Browse the marketplace', body: 'Switch between ChatGPT Plus / Pro, Claude, Grok, DeepSeek, and other groups by task.' },
  ],
  code: `curl https://tokenflux.dev/v1/chat/completions \\
  -H "Authorization: Bearer $TOKENFLUX_KEY" \\
  -d '{
    "model": "GPT-5.3 Codex",
    "messages": [{ "role": "user", "content": "Build with me." }]
  }'`,
  modelsEyebrow: 'Model Marketplace',
  modelsTitle: 'See the marketplace clearly before using it',
  modelNote: 'More models and groups are joining. The marketplace will keep growing.',
  modelGroups: [
    {
      platform: 'OpenAI compatible',
      name: 'ChatGPT Plus / Pro',
      count: '15 models',
      examples: 'codex-auto-review / GPT-5.2 / GPT-5.3 Codex',
      logo: `${iconBase}/openai.png`,
    },
    {
      platform: 'Anthropic',
      name: 'Claude',
      count: '3 models',
      examples: 'Claude Code / Sonnet',
      logo: `${iconBase}/claude-color.png`,
    },
    {
      platform: 'OpenAI compatible',
      name: 'Grok',
      count: '1 model',
      examples: 'grok-4.20 reasoning',
      logo: `${iconBase}/grok.png`,
    },
    {
      platform: 'Anthropic',
      name: 'DeepSeek',
      count: '2 models',
      examples: 'DeepSeek V4, with more models joining',
      logo: `${iconBase}/deepseek-color.png`,
    },
  ],
  cultureEyebrow: 'Origin',
  cultureTitle: 'Let tokens flow',
  cultureBody: 'Tokens were expensive, so the goal was a cheaper and more transparent gateway. That simple.',
}

const copy = computed(() => (locale.value.startsWith('zh') ? zhHomeCopy : enHomeCopy))

onMounted(() => {
  stableRunTimer = window.setInterval(() => {
    now.value = Date.now()
  }, 1000)
  updateScrollProgress()
  window.addEventListener('scroll', requestScrollProgress, { passive: true })
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})

onUnmounted(() => {
  if (stableRunTimer) {
    window.clearInterval(stableRunTimer)
  }
  if (scrollRaf) {
    window.cancelAnimationFrame(scrollRaf)
  }
  window.removeEventListener('scroll', requestScrollProgress)
})
</script>

<style scoped>
.tf-site {
  --tf-hero-y: 0px;
  --tf-hero-bg-scale-y: 1;
  --tf-hero-bg-radius: 0rem;
  --tf-hero-bg-blur: 0px;
  --tf-hero-bg-jelly: 0px;
  --tf-hero-bg-scale-x: 1;
  --tf-hero-shell-y: 0px;
  --tf-hero-shell-shadow: 0.12;
  --tf-liquid-opacity: 0;
  --tf-liquid-lip-y: 0px;
  --tf-liquid-lip-scale-x: 1;
  --tf-liquid-lip-scale-y: 1;
  --tf-liquid-drop-x: -28vw;
  --tf-liquid-drop-y: 28px;
  --tf-liquid-drop-scale: 0.32;
  --tf-line-scale-x: 1;
  --tf-hero-title-scale-x: 1;
  --tf-hero-title-scale-y: 1;
  --tf-hero-title-x: 0vw;
  --tf-hero-title-y: 0px;
  --tf-hero-title-opacity: 1;
  --tf-hero-lead-opacity: 1;
  --tf-hero-lead-y: 0px;
  --tf-hero-sub-opacity: 1;
  --tf-hero-sub-y: 0px;
  --tf-hero-badge-opacity: 1;
  --tf-hero-badge-y: 0px;
  --tf-hero-actions-y: 0px;
  --tf-hero-actions-scale: 1;
  --tf-hero-actions-gap: 0.8rem;
  --tf-hero-actions-opacity: 1;
  --tf-nav-y: -112%;
  --tf-nav-opacity: 0;
  --tf-nav-blur: 8px;
  --tf-cue-opacity: 1;
  --tf-cue-y: 0px;
  --tf-backdrop-tilt: 0deg;
  --tf-backdrop-y: 0px;
  --tf-pointer-x: 50%;
  --tf-pointer-y: 50%;
  --tf-title-flow-x: 50%;
  --tf-title-flow-y: 50%;
  --ease-jelly: cubic-bezier(0.34, 1.56, 0.64, 1);
  --ease-squish: cubic-bezier(0.68, -0.55, 0.27, 1.55);
  position: relative;
  min-height: 100vh;
  overflow-x: hidden;
  overflow-y: visible;
  color: #08111f;
  background: transparent;
}

.dark .tf-site {
  color: #f7fbff;
  background: transparent;
}

.tf-backdrop {
  position: fixed;
  inset: 0;
  pointer-events: none;
  overflow: hidden;
  transform: none;
}

.tf-line {
  position: absolute;
  left: -18vw;
  width: 138vw;
  height: 1px;
  transform: rotate(-9deg);
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.68), rgba(0, 147, 225, 0.28), transparent);
  animation: drift-line 10s ease-in-out infinite alternate;
}

.tf-line-a {
  top: 18%;
}

.tf-line-b {
  top: 47%;
  animation-delay: -3s;
}

.tf-line-c {
  top: 78%;
  animation-delay: -6s;
}

.tf-noise {
  position: absolute;
  inset: 0;
  opacity: 0.28;
  background-image: radial-gradient(rgba(0, 94, 148, 0.16) 1px, transparent 1px);
  background-size: 18px 18px;
  mask-image: linear-gradient(180deg, transparent, #000 12%, #000 76%, transparent);
}

.tf-header {
  position: fixed;
  top: 0;
  z-index: 30;
  width: 100%;
  padding: 1.25rem 1.5rem;
  opacity: var(--tf-nav-opacity);
  transform: translateY(var(--tf-nav-y));
  transition: transform 120ms var(--ease-jelly, cubic-bezier(0.34, 1.56, 0.64, 1)), opacity 120ms ease-out;
  pointer-events: auto;
}

.tf-nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: min(1180px, 100%);
  margin: 0 auto;
  padding: 0.75rem 0.875rem;
  border: 1px solid rgba(255, 255, 255, 0.74);
  border-radius: 1.25rem;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.82), rgba(232, 249, 255, 0.62)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.62), transparent 34%);
  box-shadow: 0 14px 34px rgba(20, 92, 130, 0.1), inset 0 1px 0 rgba(255, 255, 255, 0.9);
  backdrop-filter: blur(var(--tf-nav-blur)) saturate(150%);
}

.dark .tf-nav {
  border-color: rgba(144, 215, 255, 0.14);
  background: rgba(8, 16, 31, 0.88);
  box-shadow: 0 16px 46px rgba(0, 0, 0, 0.28), inset 0 1px 0 rgba(255, 255, 255, 0.08);
}

.tf-brand,
.tf-nav-actions,
.tf-hero-actions,
.tf-footer div {
  display: flex;
  align-items: center;
}

.tf-brand {
  min-width: 0;
  gap: 0.75rem;
  text-decoration: none;
}

.tf-brand-mark {
  display: grid;
  width: 2.5rem;
  height: 2.5rem;
  place-items: center;
  overflow: hidden;
  border-radius: 0.875rem;
  background: #07101f;
}

.tf-brand-mark img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.tf-brand-copy {
  display: grid;
  gap: 0.05rem;
  color: inherit;
}

.tf-brand-copy strong {
  font-size: 0.95rem;
  line-height: 1;
}

.tf-brand-copy small {
  color: rgba(8, 17, 31, 0.52);
  font-size: 0.72rem;
}

.dark .tf-brand-copy small {
  color: rgba(247, 251, 255, 0.58);
}

.tf-nav-actions {
  gap: 0.45rem;
}

.tf-nav-link {
  display: inline-flex;
  align-items: center;
  min-height: 2.35rem;
  border-radius: 999px;
  padding: 0 0.85rem;
  color: rgba(8, 17, 31, 0.66);
  font-size: 0.86rem;
  font-weight: 750;
  text-decoration: none;
  transition: background-color 180ms ease, color 180ms ease;
}

.tf-nav-link:hover {
  background: rgba(0, 126, 205, 0.08);
  color: #07101f;
}

.dark .tf-nav-link {
  color: rgba(247, 251, 255, 0.7);
}

.tf-icon-link,
.tf-login {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 2.35rem;
  border: 0;
  border-radius: 999px;
  color: inherit;
  text-decoration: none;
  transition: transform 180ms ease, background-color 180ms ease;
}

.tf-icon-link {
  width: 2.35rem;
  background: transparent;
}

.tf-icon-link:hover,
.tf-login:hover {
  transform: translateY(-1px);
  background: rgba(0, 126, 205, 0.08);
}

.tf-login {
  gap: 0.4rem;
  padding: 0 1rem;
  background: #07101f;
  color: #ffffff;
  font-size: 0.82rem;
  font-weight: 700;
}

.tf-login-user span {
  display: grid;
  width: 1.35rem;
  height: 1.35rem;
  place-items: center;
  border-radius: 999px;
  background: #00a9ee;
  font-size: 0.7rem;
}

.tf-main {
  position: relative;
  z-index: 2;
}

.tf-hero {
  position: relative;
  display: grid;
  min-height: 100svh;
  place-items: center;
  overflow: clip;
  padding: 8.5rem 1.5rem 5rem;
  isolation: isolate;
  transform: translateY(var(--tf-hero-y));
  transform-origin: center top;
}

.tf-hero::before,
.tf-hero::after {
  content: '';
  position: absolute;
  left: 50%;
  pointer-events: none;
  opacity: var(--tf-liquid-opacity);
  z-index: 1;
}

.tf-hero::before {
  bottom: -2.8rem;
  width: min(48rem, 70vw);
  height: 7.5rem;
  border-radius: 48% 52% 50% 50% / 74% 76% 26% 24%;
  background:
    radial-gradient(ellipse at 35% 18%, rgba(255, 255, 255, 0.74), transparent 34%),
    linear-gradient(180deg, rgba(205, 247, 255, 0.78), rgba(44, 190, 237, 0.28));
  filter: blur(0.5px);
  transform:
    translateX(-50%)
    translateY(var(--tf-liquid-lip-y))
    scaleX(var(--tf-liquid-lip-scale-x))
    scaleY(var(--tf-liquid-lip-scale-y));
  transform-origin: center top;
  box-shadow: 0 -16px 40px rgba(255, 255, 255, 0.28), 0 22px 70px rgba(0, 151, 220, 0.16);
}

.tf-hero::after {
  display: none;
  top: 12.5rem;
  width: 8.5rem;
  height: 8.5rem;
  border-radius: 56% 44% 62% 38% / 42% 58% 42% 58%;
  background:
    radial-gradient(circle at 34% 24%, rgba(255, 255, 255, 0.78), transparent 24%),
    radial-gradient(circle at 70% 74%, rgba(0, 169, 238, 0.32), transparent 46%),
    rgba(206, 247, 255, 0.62);
  filter: blur(0.3px);
  transform:
    translateX(var(--tf-liquid-drop-x))
    translateY(var(--tf-liquid-drop-y))
    scale(var(--tf-liquid-drop-scale));
  transform-origin: center;
  box-shadow: 0 16px 48px rgba(0, 151, 220, 0.14);
}

.tf-swipe-cue {
  position: fixed;
  left: 50%;
  bottom: 1.8rem;
  z-index: 24;
  display: inline-flex;
  align-items: center;
  gap: 0.55rem;
  border: 1px solid rgba(255, 255, 255, 0.68);
  border-radius: 999px;
  padding: 0.7rem 1rem;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.78), rgba(232, 249, 255, 0.52)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.58), transparent 36%);
  color: #07101f;
  font-size: 0.86rem;
  font-weight: 850;
  text-decoration: none;
  box-shadow: 0 18px 50px rgba(20, 92, 130, 0.16);
  opacity: var(--tf-cue-opacity);
  transform: translateX(-50%) translateY(var(--tf-cue-y));
  backdrop-filter: blur(18px) saturate(150%);
  pointer-events: auto;
}

.tf-swipe-cue :deep(svg) {
  transform: rotate(-90deg);
  animation: swipe-arrow 1.25s ease-in-out infinite;
}

.dark .tf-swipe-cue {
  border-color: rgba(137, 217, 255, 0.16);
  background: rgba(8, 16, 31, 0.82);
  color: #ffffff;
}

.tf-hero-visual {
  position: absolute;
  inset: 6.25rem max(1.5rem, calc((100vw - 1180px) / 2)) 3.2rem;
  border-radius: var(--tf-hero-bg-radius);
  overflow: hidden;
  transform:
    translateY(calc(var(--tf-hero-shell-y) + var(--tf-hero-bg-jelly) * -0.25))
    scaleX(var(--tf-hero-bg-scale-x))
    scaleY(var(--tf-hero-bg-scale-y));
  transform-origin: center top;
  background:
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.88), transparent 22%),
    radial-gradient(ellipse at 50% calc(100% - var(--tf-hero-bg-jelly)), rgba(255, 255, 255, 0.74), transparent 28%),
    linear-gradient(145deg, rgba(255, 255, 255, 0.86), rgba(236, 250, 255, 0.58)),
    radial-gradient(ellipse at 52% 42%, rgba(255, 255, 255, 0.66), rgba(207, 247, 255, 0.38) 52%, rgba(132, 224, 250, 0.12) 82%);
  box-shadow:
    inset 0 0 0 1px rgba(255, 255, 255, 0.86),
    inset 0 18px 48px rgba(255, 255, 255, 0.28),
    0 34px 88px rgba(22, 115, 166, var(--tf-hero-shell-shadow));
  backdrop-filter: blur(var(--tf-hero-bg-blur));
}

.dark .tf-hero-visual {
  background:
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(137, 217, 255, 0.14), transparent 24%),
    radial-gradient(ellipse at 50% calc(100% - var(--tf-hero-bg-jelly)), rgba(137, 217, 255, 0.16), transparent 30%),
    linear-gradient(145deg, rgba(10, 22, 40, 0.9), rgba(14, 31, 54, 0.76)),
    linear-gradient(180deg, rgba(0, 164, 236, 0.12), rgba(255, 255, 255, 0.03));
  box-shadow: inset 0 0 0 1px rgba(137, 217, 255, 0.12), 0 38px 110px rgba(0, 0, 0, 0.34);
}

.tf-hero-visual::before,
.tf-hero-visual::after {
  content: '';
  position: absolute;
  inset: 13% -8%;
  border: 1px solid rgba(0, 147, 225, 0.16);
  transform: translateY(var(--tf-hero-bg-jelly)) skewY(-12deg) scaleX(var(--tf-line-scale-x));
  animation: sheet-shift 9s ease-in-out infinite alternate;
}

.tf-hero-visual::after {
  inset: 22% -10%;
  border-color: rgba(0, 147, 225, 0.1);
  animation-delay: -4s;
}

.tf-circuit {
  position: absolute;
  inset: auto 2rem 2rem;
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 0.65rem;
}

.tf-circuit span,
.tf-orbit {
  border: 1px solid rgba(0, 127, 205, 0.16);
  background: rgba(255, 255, 255, 0.62);
  color: rgba(8, 17, 31, 0.62);
  backdrop-filter: blur(14px);
}

.dark .tf-circuit span,
.dark .tf-orbit {
  border-color: rgba(137, 217, 255, 0.14);
  background: rgba(7, 16, 31, 0.58);
  color: rgba(247, 251, 255, 0.72);
}

.tf-circuit span {
  border-radius: 999px;
  padding: 0.55rem 0.85rem;
  font-size: 0.78rem;
}

.tf-orbit {
  position: absolute;
  border-radius: 0.9rem;
  padding: 0.75rem 1rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.82rem;
  animation: float-label 6s ease-in-out infinite alternate;
}

.tf-orbit-a {
  top: 18%;
  left: 9%;
}

.tf-orbit-b {
  top: 22%;
  right: 12%;
  animation-delay: -1.4s;
}

.tf-orbit-c {
  right: 20%;
  bottom: 28%;
  animation-delay: -2.8s;
}

.tf-orbit-d {
  left: 16%;
  bottom: 28%;
  animation-delay: -4.2s;
}

.tf-hero-content {
  position: relative;
  width: min(1080px, 100%);
  overflow: visible;
  text-align: center;
  transform-origin: center top;
}

.tf-kicker {
  display: inline-flex;
  margin-bottom: 1.25rem;
  border: 1px solid rgba(0, 127, 205, 0.16);
  border-radius: 999px;
  padding: 0.65rem 1rem;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.78), rgba(232, 249, 255, 0.56)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.56), transparent 34%);
  color: #006fb3;
  font-size: 0.88rem;
  font-weight: 800;
  opacity: var(--tf-hero-badge-opacity);
  transform: translateY(var(--tf-hero-badge-y));
  backdrop-filter: blur(16px);
}

.dark .tf-kicker {
  background: rgba(8, 16, 31, 0.84);
  color: #83dbff;
}

.tf-hero-title {
  margin: -0.08em auto -0.02em;
  padding: 0.08em 0.08em 0.14em;
  font-size: clamp(3.8rem, 9.4vw, 8rem);
  font-weight: 950;
  line-height: 1;
  letter-spacing: 0;
  color: transparent;
  background:
    radial-gradient(circle at var(--tf-title-flow-x) var(--tf-title-flow-y), rgba(255, 255, 255, 0.82) 0 4%, #69dfff 15%, rgba(36, 193, 127, 0.42) 27%, transparent 44%),
    linear-gradient(105deg, #061225 0%, #0074b7 26%, #24c17f 48%, #07101f 72%, #061225 100%);
  background-size: 320% 280%, 240% 100%;
  background-position: var(--tf-title-flow-x) var(--tf-title-flow-y), 0% 50%;
  -webkit-background-clip: text;
  background-clip: text;
  filter: drop-shadow(0 18px 38px rgba(0, 116, 183, 0.12));
  animation: token-title-flow 12s ease-in-out infinite alternate;
  opacity: var(--tf-hero-title-opacity);
  transform:
    translateX(var(--tf-hero-title-x))
    translateY(var(--tf-hero-title-y))
    scaleX(var(--tf-hero-title-scale-x))
    scaleY(var(--tf-hero-title-scale-y));
  transform-origin: center top;
  will-change: transform, opacity;
}

.tf-hero-lead {
  margin: 1.5rem auto 0;
  max-width: 760px;
  color: rgba(8, 17, 31, 0.84);
  font-size: clamp(1.7rem, 3vw, 3.1rem);
  font-weight: 780;
  line-height: 1.08;
  opacity: var(--tf-hero-lead-opacity);
  transform: translateY(var(--tf-hero-lead-y));
  transform-origin: center top;
}

.dark .tf-hero-lead {
  color: rgba(247, 251, 255, 0.92);
}

.dark .tf-hero-title {
  background:
    radial-gradient(circle at var(--tf-title-flow-x) var(--tf-title-flow-y), rgba(255, 255, 255, 0.84) 0 4%, #83dbff 18%, rgba(36, 193, 127, 0.42) 30%, transparent 46%),
    linear-gradient(105deg, #f7fbff 0%, #83dbff 30%, #24c17f 52%, #f7fbff 100%);
  background-size: 320% 280%, 240% 100%;
  -webkit-background-clip: text;
  background-clip: text;
}

.tf-hero-sub {
  margin: 1.35rem auto 0;
  max-width: 720px;
  color: rgba(8, 17, 31, 0.62);
  font-size: 1.08rem;
  line-height: 1.85;
  opacity: var(--tf-hero-sub-opacity);
  transform: translateY(var(--tf-hero-sub-y));
  transform-origin: center top;
}

.dark .tf-hero-sub {
  color: rgba(247, 251, 255, 0.66);
}

.tf-hero-actions {
  justify-content: center;
  gap: var(--tf-hero-actions-gap);
  margin-top: 2rem;
  flex-wrap: wrap;
  opacity: var(--tf-hero-actions-opacity);
  transform: translateY(var(--tf-hero-actions-y)) scale(var(--tf-hero-actions-scale));
  transform-origin: center top;
  will-change: transform, opacity;
}

.tf-primary,
.tf-secondary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.55rem;
  min-height: 3.2rem;
  border-radius: 999px;
  padding: 0 1.35rem;
  font-weight: 800;
  text-decoration: none;
  transition: transform 180ms var(--ease-jelly), box-shadow 180ms ease, background-color 180ms ease;
}

.tf-primary {
  background: #07101f;
  color: #ffffff;
  box-shadow: 0 18px 42px rgba(7, 16, 31, 0.22);
}

.tf-secondary {
  border: 1px solid rgba(0, 127, 205, 0.16);
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.78), rgba(232, 249, 255, 0.52)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.5), transparent 36%);
  color: #07101f;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.76);
}

.dark .tf-secondary {
  border-color: rgba(137, 217, 255, 0.18);
  background: rgba(8, 16, 31, 0.82);
  color: #ffffff;
}

.tf-primary:hover,
.tf-secondary:hover {
  transform: translateY(-3px) scale(1.018);
}

.tf-proof-strip,
.tf-story,
.tf-scroll-stage,
.tf-models,
.tf-culture,
.tf-footer {
  width: min(1180px, calc(100% - 3rem));
  margin: 0 auto;
}

.tf-proof-strip {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.75rem;
  margin-top: -2rem;
  padding: 0.9rem;
  border: 1px solid rgba(0, 127, 205, 0.12);
  border-radius: 1.25rem;
  background: rgba(255, 255, 255, 0.74);
  box-shadow: 0 22px 70px rgba(22, 115, 166, 0.12);
  backdrop-filter: blur(20px);
}

.dark .tf-proof-strip {
  border-color: rgba(137, 217, 255, 0.12);
  background: rgba(8, 16, 31, 0.76);
}

.tf-proof-item {
  min-width: 0;
  padding: 1rem;
}

.tf-proof-item span,
.tf-section-heading p,
.tf-model-card p,
.tf-culture p:first-child {
  color: #0074b7;
  font-size: 0.75rem;
  font-weight: 900;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.tf-model-card p {
  letter-spacing: 0;
  text-transform: none;
}

.tf-proof-item strong {
  display: block;
  margin-top: 0.35rem;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.92rem;
}

.tf-story,
.tf-scroll-stage,
.tf-models,
.tf-culture {
  padding: 7rem 0 0;
}

.tf-section-heading {
  max-width: 780px;
}

.tf-section-heading h2,
.tf-culture h2 {
  margin: 0.8rem 0 0;
  font-size: clamp(2.35rem, 5vw, 5.1rem);
  font-weight: 850;
  line-height: 0.98;
  letter-spacing: 0;
}

.tf-about-panel {
  display: grid;
  grid-template-columns: minmax(0, 1.25fr) minmax(16rem, 0.75fr);
  gap: 1rem;
  margin-top: 2rem;
  border: 1px solid rgba(0, 127, 205, 0.12);
  border-radius: 1.25rem;
  background:
    linear-gradient(145deg, rgba(255, 255, 255, 0.78), rgba(232, 249, 255, 0.54)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.46), transparent 36%);
  box-shadow: 0 14px 34px rgba(20, 92, 130, 0.08), inset 0 1px 0 rgba(255, 255, 255, 0.86);
  padding: 1.6rem;
  backdrop-filter: blur(18px);
}

.dark .tf-about-panel {
  border-color: rgba(137, 217, 255, 0.12);
  background: rgba(8, 16, 31, 0.86);
}

.tf-about-copy {
  display: grid;
  gap: 1rem;
}

.tf-about-copy p {
  margin: 0;
  color: rgba(8, 17, 31, 0.68);
  font-size: 1rem;
  line-height: 1.8;
}

.dark .tf-about-copy p {
  color: rgba(247, 251, 255, 0.68);
}

.tf-growth-panel {
  display: grid;
  min-width: 0;
  gap: 1rem;
  overflow: hidden;
  border-radius: 1rem;
  background:
    linear-gradient(150deg, rgba(255, 255, 255, 0.78), rgba(230, 250, 255, 0.5)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.56), transparent 34%),
    radial-gradient(ellipse at 92% 10%, rgba(0, 169, 238, 0.18), transparent 48%);
  padding: 1.1rem;
  box-shadow: inset 0 0 0 1px rgba(0, 127, 205, 0.09);
}

.dark .tf-growth-panel {
  background:
    linear-gradient(150deg, rgba(10, 22, 40, 0.9), rgba(14, 31, 54, 0.82)),
    radial-gradient(ellipse at 92% 10%, rgba(0, 169, 238, 0.16), transparent 48%);
  box-shadow: inset 0 0 0 1px rgba(137, 217, 255, 0.11);
}

.tf-growth-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.8rem;
}

.tf-growth-top span {
  color: #0074b7;
  font-size: 0.75rem;
  font-weight: 900;
}

.tf-growth-top strong {
  color: #07101f;
  font-size: 1.05rem;
}

.dark .tf-growth-top strong {
  color: #ffffff;
}

.tf-growth-stats {
  display: grid;
  grid-template-columns: minmax(0, 1.05fr) minmax(0, 0.95fr);
  gap: 0.55rem;
}

.tf-growth-stat {
  min-width: 0;
  border-radius: 0.85rem;
  background: rgba(255, 255, 255, 0.64);
  padding: 0.8rem;
}

.dark .tf-growth-stat {
  background: rgba(255, 255, 255, 0.08);
}

.tf-growth-stats span,
.tf-growth-panel p {
  color: rgba(8, 17, 31, 0.58);
  font-size: 0.78rem;
  line-height: 1.5;
}

.dark .tf-growth-stats span,
.dark .tf-growth-panel p {
  color: rgba(247, 251, 255, 0.64);
}

.tf-growth-stats strong {
  display: block;
  margin-top: 0.35rem;
  overflow-wrap: anywhere;
  color: #07101f;
  font-size: clamp(1.2rem, 2.5vw, 1.8rem);
  line-height: 1;
}

.tf-growth-stat small {
  display: block;
  margin-top: 0.45rem;
  color: rgba(8, 17, 31, 0.48);
  font-size: 0.75rem;
}

.dark .tf-growth-stat small {
  color: rgba(247, 251, 255, 0.52);
}

.dark .tf-growth-stats strong {
  color: #ffffff;
}

.tf-clock-ticks {
  display: flex;
  gap: 0.35rem;
  margin-top: 0.6rem;
}

.tf-clock-ticks span {
  display: grid;
  min-width: 2.05rem;
  min-height: 1.55rem;
  place-items: center;
  border-radius: 0.5rem;
  background: rgba(0, 169, 238, 0.1);
  color: #0074b7;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.82rem;
  font-weight: 900;
  animation: clock-breathe 1s ease-in-out infinite;
}

.tf-clock-ticks span:nth-child(2) {
  animation-delay: 120ms;
}

.tf-clock-ticks span:nth-child(3) {
  animation-delay: 240ms;
}

.dark .tf-clock-ticks span {
  background: rgba(131, 219, 255, 0.12);
  color: #83dbff;
}

.tf-growth-chart {
  position: relative;
  min-height: 9rem;
  overflow: hidden;
  border-radius: 1rem;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.68), rgba(255, 255, 255, 0.36)),
    repeating-linear-gradient(0deg, rgba(0, 127, 205, 0.08) 0 1px, transparent 1px 2.2rem);
}

.dark .tf-growth-chart {
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.08), rgba(255, 255, 255, 0.045)),
    repeating-linear-gradient(0deg, rgba(137, 217, 255, 0.09) 0 1px, transparent 1px 2.2rem);
}

.tf-chart-bars {
  position: absolute;
  inset: 1rem 1rem 1rem;
  display: flex;
  align-items: end;
  justify-content: space-between;
  gap: 0.42rem;
}

.tf-chart-bars span {
  width: 100%;
  border-radius: 999px 999px 0 0;
  background: linear-gradient(180deg, #00a9ee, #24c17f);
  transform: scaleY(0.2);
  transform-origin: bottom;
  animation: bar-rise 900ms cubic-bezier(0.22, 1, 0.36, 1) forwards;
}

.tf-chart-bars span:nth-child(2) {
  animation-delay: 80ms;
}

.tf-chart-bars span:nth-child(3) {
  animation-delay: 160ms;
}

.tf-chart-bars span:nth-child(4) {
  animation-delay: 240ms;
}

.tf-chart-bars span:nth-child(5) {
  animation-delay: 320ms;
}

.tf-chart-bars span:nth-child(6) {
  animation-delay: 400ms;
}

.tf-chart-bars span:nth-child(7) {
  animation-delay: 480ms;
}

.tf-growth-chart svg {
  position: absolute;
  inset: 0.6rem;
  width: calc(100% - 1.2rem);
  height: calc(100% - 1.2rem);
  fill: none;
}

.tf-growth-chart polyline {
  stroke: #07101f;
  stroke-dasharray: 360;
  stroke-dashoffset: 360;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 4;
  animation: line-draw 1200ms 240ms cubic-bezier(0.22, 1, 0.36, 1) forwards;
}

.dark .tf-growth-chart polyline {
  stroke: #dff7ff;
}

.tf-growth-panel p {
  margin: 0;
}

.tf-story-grid,
.tf-model-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1rem;
  margin-top: 2rem;
}

.tf-story-card,
.tf-model-card,
.tf-culture {
  border: 1px solid rgba(0, 127, 205, 0.12);
  border-radius: 1.25rem;
  background:
    linear-gradient(145deg, rgba(255, 255, 255, 0.78), rgba(232, 249, 255, 0.54)),
    radial-gradient(circle at var(--tf-pointer-x) var(--tf-pointer-y), rgba(255, 255, 255, 0.42), transparent 34%);
  box-shadow: 0 14px 34px rgba(20, 92, 130, 0.08), inset 0 1px 0 rgba(255, 255, 255, 0.82);
  backdrop-filter: blur(18px);
  transition: transform 220ms var(--ease-jelly), box-shadow 220ms ease, border-color 220ms ease;
}

.dark .tf-story-card,
.dark .tf-model-card,
.dark .tf-culture {
  border-color: rgba(137, 217, 255, 0.12);
  background: rgba(8, 16, 31, 0.86);
}

.tf-story-card {
  min-height: 17rem;
  padding: 1.6rem;
}

.tf-story-card:hover,
.tf-model-card:hover,
.tf-growth-panel:hover,
.tf-culture:hover {
  border-color: rgba(0, 127, 205, 0.2);
  box-shadow: 0 18px 44px rgba(20, 92, 130, 0.13), inset 0 1px 0 rgba(255, 255, 255, 0.88);
  transform: translateY(-5px) scale(1.006);
}

.tf-story-card span {
  display: grid;
  width: 3.2rem;
  height: 3.2rem;
  place-items: center;
  border-radius: 0.9rem;
  background: #07101f;
  color: #ffffff;
  font-weight: 900;
}

.tf-story-card h3,
.tf-step h3,
.tf-model-card h3 {
  margin: 1.3rem 0 0;
  font-size: 1.35rem;
}

.tf-story-card p,
.tf-step p,
.tf-culture p:last-child {
  color: rgba(8, 17, 31, 0.62);
  line-height: 1.8;
}

.dark .tf-story-card p,
.dark .tf-step p,
.dark .tf-culture p:last-child {
  color: rgba(247, 251, 255, 0.66);
}

.tf-stage-grid {
  display: grid;
  grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.1fr);
  gap: 3rem;
  align-items: start;
  margin-top: 2.25rem;
}

.tf-stage-copy {
  display: grid;
  gap: 1rem;
}

.tf-step {
  min-height: 12rem;
  padding: 1.5rem 0;
  border-top: 1px solid rgba(0, 127, 205, 0.14);
}

.tf-step span {
  display: inline-grid;
  width: 2rem;
  height: 2rem;
  place-items: center;
  border-radius: 999px;
  background: rgba(0, 153, 224, 0.12);
  color: #0074b7;
  font-weight: 900;
}

.tf-code-stage {
  position: sticky;
  top: 7.5rem;
  overflow: hidden;
  border: 1px solid rgba(7, 16, 31, 0.08);
  border-radius: 1.25rem;
  background: #07101f;
  color: #dff7ff;
  box-shadow: 0 30px 90px rgba(7, 16, 31, 0.28);
  transition: transform 220ms var(--ease-jelly), box-shadow 220ms ease;
}

.tf-code-stage:hover {
  box-shadow: 0 34px 100px rgba(7, 16, 31, 0.34);
  transform: translateY(-4px);
}

.tf-code-title {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.95rem 1.2rem;
  background: rgba(255, 255, 255, 0.08);
  color: rgba(255, 255, 255, 0.62);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.78rem;
}

.tf-code-title span {
  width: 0.72rem;
  height: 0.72rem;
  border-radius: 999px;
  background: #ff5d5d;
}

.tf-code-title span:nth-child(2) {
  background: #ffbd3d;
}

.tf-code-title span:nth-child(3) {
  background: #2fd16b;
  margin-right: auto;
}

.tf-code-stage pre {
  margin: 0;
  padding: 1.5rem;
  overflow-x: auto;
  color: #dff7ff;
  font-size: 0.9rem;
  line-height: 1.8;
}

.tf-code-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  padding: 0 1.5rem 1.5rem;
}

.tf-code-tags span {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  border: 1px solid rgba(131, 219, 255, 0.18);
  border-radius: 999px;
  padding: 0.45rem 0.7rem;
  color: rgba(223, 247, 255, 0.72);
  font-size: 0.75rem;
}

.tf-code-tags img {
  width: 1rem;
  height: 1rem;
  object-fit: contain;
}

.tf-model-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.tf-model-card {
  position: relative;
  min-height: 16rem;
  padding: 1.35rem;
  overflow: hidden;
}

.tf-model-card-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.tf-model-card-top > div {
  min-width: 0;
}

.tf-model-card-top h3 {
  margin-top: 1rem;
}

.tf-model-logo {
  display: grid;
  width: 2.65rem;
  height: 2.65rem;
  place-items: center;
  flex: 0 0 auto;
  border: 1px solid rgba(0, 127, 205, 0.12);
  border-radius: 0.9rem;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.78), rgba(232, 249, 255, 0.5));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.82);
}

.dark .tf-model-logo {
  border-color: rgba(137, 217, 255, 0.14);
  background: rgba(255, 255, 255, 0.1);
}

.tf-model-logo img {
  width: 1.65rem;
  height: 1.65rem;
  object-fit: contain;
}

.tf-model-card::after {
  content: '';
  position: absolute;
  inset: auto 0 0;
  height: 4px;
  background: linear-gradient(90deg, #00a9ee, #24c17f, #f0b429);
  transform: scaleX(0.3);
  transform-origin: left;
  transition: transform 220ms ease;
}

.tf-model-card:hover::after {
  transform: scaleX(1);
}

.tf-model-card strong {
  display: block;
  margin-top: 4.5rem;
  font-size: 2rem;
}

.tf-model-card > span {
  display: block;
  margin-top: 0.85rem;
  color: rgba(8, 17, 31, 0.6);
  line-height: 1.6;
}

.dark .tf-model-card > span {
  color: rgba(247, 251, 255, 0.64);
}

.tf-model-note {
  margin: 1.1rem 0 0;
  color: rgba(8, 17, 31, 0.66);
  font-size: 1rem;
  font-weight: 760;
}

.dark .tf-model-note {
  color: rgba(247, 251, 255, 0.7);
}

.tf-culture {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 1.2rem;
  align-items: center;
  margin-top: 2.5rem;
  margin-bottom: 5rem;
  padding: 1.7rem 2rem;
}

.tf-culture-mark {
  font-size: clamp(2.75rem, 7vw, 5rem);
  line-height: 1;
  filter: drop-shadow(0 20px 40px rgba(0, 127, 205, 0.18));
  animation: soft-pop 5s ease-in-out infinite alternate;
}

.tf-culture h2 {
  font-size: clamp(1.85rem, 3.4vw, 3.25rem);
}

.tf-footer {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 2rem 0 3rem;
  color: rgba(8, 17, 31, 0.58);
  font-size: 0.9rem;
}

.dark .tf-footer {
  color: rgba(247, 251, 255, 0.58);
}

.tf-footer div {
  gap: 1rem;
}

.tf-footer a {
  color: inherit;
  text-decoration: none;
}

@keyframes drift-line {
  from {
    transform: translateX(-2%) rotate(-9deg);
  }
  to {
    transform: translateX(3%) rotate(-9deg);
  }
}

@keyframes sheet-shift {
  from {
    transform: translateX(-1.5%) skewY(-12deg);
  }
  to {
    transform: translateX(1.5%) skewY(-12deg);
  }
}

@keyframes float-label {
  from {
    transform: translateY(0);
  }
  to {
    transform: translateY(-12px);
  }
}

@keyframes soft-pop {
  from {
    transform: rotate(-2deg) scale(0.98);
  }
  to {
    transform: rotate(2deg) scale(1.02);
  }
}

@keyframes token-title-flow {
  0% {
    background-position: var(--tf-title-flow-x) var(--tf-title-flow-y), 4% 50%;
  }
  50% {
    background-position:
      calc(var(--tf-title-flow-x) + 8%) calc(var(--tf-title-flow-y) + 6%),
      52% 50%;
  }
  100% {
    background-position:
      calc(100% - var(--tf-title-flow-x)) calc(100% - var(--tf-title-flow-y)),
      96% 50%;
  }
}

@keyframes swipe-arrow {
  0%,
  100% {
    transform: translateY(0) rotate(-90deg);
  }
  50% {
    transform: translateY(-6px) rotate(-90deg);
  }
}

@keyframes clock-breathe {
  0%,
  100% {
    transform: translateY(0);
  }
  50% {
    transform: translateY(-2px);
  }
}

@keyframes bar-rise {
  to {
    transform: scaleY(1);
  }
}

@keyframes line-draw {
  to {
    stroke-dashoffset: 0;
  }
}

@media (max-width: 900px) {
  .tf-header {
    padding: 0.8rem;
  }

  .tf-brand-copy {
    display: none;
  }

  .tf-nav {
    border-radius: 1rem;
  }

  .tf-hero {
    min-height: 100svh;
    padding: 7.5rem 1rem 4rem;
  }

  .tf-hero-visual {
    inset: 5.5rem 0.8rem 2rem;
    border-radius: var(--tf-hero-bg-radius);
  }

  .tf-hero::before {
    width: 78vw;
    height: 5.5rem;
  }

  .tf-hero::after {
    top: 11rem;
    width: 6rem;
    height: 6rem;
  }

  .tf-orbit {
    display: none;
  }

  .tf-hero-title {
    font-size: clamp(3.2rem, 18vw, 5.2rem);
  }

  .tf-hero-lead {
    font-size: clamp(1.55rem, 9vw, 2.45rem);
  }

  .tf-proof-strip,
  .tf-story,
  .tf-scroll-stage,
  .tf-models,
  .tf-culture,
  .tf-footer {
    width: min(100% - 1.5rem, 1180px);
  }

  .tf-proof-strip,
  .tf-about-panel,
  .tf-story-grid,
  .tf-stage-grid,
  .tf-model-grid,
  .tf-culture {
    grid-template-columns: 1fr;
  }

  .tf-growth-stats {
    grid-template-columns: 1fr;
  }

  .tf-swipe-cue {
    bottom: 1rem;
  }

  .tf-story,
  .tf-scroll-stage,
  .tf-models,
  .tf-culture {
    padding-top: 5rem;
  }

  .tf-code-stage {
    position: relative;
    top: 0;
  }

  .tf-culture {
    gap: 1rem;
    margin-top: 3rem;
    padding: 1.5rem;
    text-align: center;
  }

  .tf-footer {
    flex-direction: column;
  }
}

@media (prefers-reduced-motion: reduce) {
  .tf-line,
  .tf-hero-visual::before,
  .tf-hero-visual::after,
  .tf-orbit,
  .tf-culture-mark,
  .tf-chart-bars span,
  .tf-growth-chart polyline,
  .tf-hero-visual,
  .tf-hero-content,
  .tf-header,
  .tf-swipe-cue,
  .tf-clock-ticks span {
    animation-duration: 1ms;
    animation-iteration-count: 1;
  }
}
</style>
