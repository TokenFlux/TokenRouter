<template>
  <header class="site-header fixed inset-x-0 top-0 z-header border-b border-primary-900/10">
    <!--
      Header padding follows the same breakpoint sequence as the main content so their
      left and right edges stay aligned.
    -->
    <div class="flex h-[var(--header-h)] items-center justify-between gap-3 px-4 md:px-6 lg:px-8">
      <!-- The global header owns site branding. -->
      <div class="flex min-w-0 shrink-0 items-center gap-2 sm:gap-4">
        <!-- The sidebar toggle stays square at widths where navigation labels begin to show. -->
        <button
          v-if="!publicPage && !isCreativeStudio"
          @click="appStore.toggleMobileSidebar()"
          class="header-nav-link md:w-9 md:px-0 lg:hidden"
          :aria-label="t('common.toggleMenu')"
          :title="t('common.toggleMenu')"
        >
          <Icon name="menu" size="md" />
        </button>

        <!-- Version details and the homepage link remain separate controls. -->
        <div class="header-brand group/brand flex min-w-0 items-center gap-2.5 px-1.5 py-1">
          <router-link
            :to="homePath"
            class="flex h-8 w-8 shrink-0 items-center justify-center overflow-hidden rounded-control bg-primary-100 dark:bg-dark-800"
            :aria-label="siteName"
          >
            <img
              v-if="settingsLoaded"
              :src="siteLogo || '/logo.svg'"
              :alt="siteName"
              class="h-full w-full object-contain"
            />
          </router-link>
          <span class="hidden min-w-0 sm:block">
            <router-link
              :to="homePath"
              class="block max-w-44 truncate text-base font-bold leading-tight text-gray-900 transition-colors duration-fast focus-visible:text-primary-600 group-hover/brand:text-primary-600 dark:text-white dark:focus-visible:text-primary-500 dark:group-hover/brand:text-primary-500"
              >{{ siteName }}</router-link
            >
            <VersionBadge :version="siteVersion" />
          </span>
        </div>

        <!-- The workspace dashboard return link sits beside the brand homepage link. -->
        <router-link
          v-if="isCreativeStudio"
          to="/dashboard"
          class="btn-ghost btn-icon shrink-0"
          :aria-label="t('creative.canvas.backToDashboard')"
          :title="t('creative.canvas.backToDashboard')"
        >
          <Icon name="home" size="md" />
        </router-link>
      </div>

      <!--
        The global search entry leads the right-hand group, followed by labeled
        navigation entries and the language switcher, the guest theme toggle, and the
        balance and avatar that form the trailing account group.
      -->
      <div class="header-status-actions">
        <GlobalSearchTrigger />

        <!-- Navigation entries show text labels from md upward and icons below md. -->
        <div class="header-nav-group">
          <router-link
            v-if="publicPage"
            to="/models"
            class="header-nav-link hidden sm:flex"
            :aria-label="t('nav.modelMarketplace')"
          >
            <Icon name="modelMarketplace" size="md" class="md:hidden" />
            <span class="hidden md:inline">{{ t('nav.modelMarketplace') }}</span>
          </router-link>

          <div v-if="user" class="hidden sm:block">
            <AnnouncementBell variant="text" />
          </div>

          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="header-nav-link hidden sm:flex"
            :aria-label="t('nav.docs')"
          >
            <Icon name="book" size="md" class="md:hidden" />
            <span class="hidden md:inline">{{ t('nav.docs') }}</span>
          </a>

          <HeaderContactSupport />

          <LocaleSwitcher variant="text" />
        </div>

        <!--
          Authenticated users select themes inside the account menu; guests retain a
          header theme toggle.
        -->
        <button
          v-if="!user"
          type="button"
          data-testid="theme-toggle"
          class="header-status-icon-button"
          :aria-label="isDark ? t('nav.lightMode') : t('nav.darkMode')"
          :title="isDark ? t('nav.lightMode') : t('nav.darkMode')"
          @click="toggleTheme"
        >
          <!-- Sun and moon colors distinguish the theme toggle states. -->
          <Icon
            :name="isDark ? 'sun' : 'moon'"
            size="md"
            :class="isDark ? 'text-amber-500' : 'text-blue-500'"
          />
        </button>

        <template v-if="user">
          <div class="header-status-divider hidden sm:block"></div>

          <!--
            The balance control opens subscription details; narrow screens show the
            balance inside the account menu.
          -->
          <SubscriptionProgressMini variant="status" class="hidden sm:block">
            <span class="text-primary-900/60 dark:text-dark-400">{{ balanceUnitSymbol }}</span>
            <span class="font-semibold tabular-nums text-primary-900 dark:text-dark-100">
              {{ formatHeaderMoney(availableBalance, false) }}
            </span>
            <span
              v-if="frozenBalance > 0"
              class="ml-1 text-xs font-medium text-amber-600 dark:text-amber-300"
              :title="balanceFrozenLabel"
            >
              {{ balanceFrozenLabel }}
            </span>
          </SubscriptionProgressMini>
        </template>

        <!-- The avatar opens the user menu and matches the utility button height. -->
        <div v-if="user" class="relative" ref="dropdownRef">
          <button
            @click="toggleDropdown"
            class="header-status-user-button"
            :aria-label="t('common.userMenu')"
          >
            <UserAvatar
              :avatar-url="avatarUrl"
              :user-id="user.id"
              :alt="displayName"
              size-class="h-9 w-9"
            />
          </button>

          <!--
            Account details, navigation, administrator actions, logout, and theme
            controls occupy separate inset menu sections.
          -->
          <MotionTransition name="dropdown-fade">
            <div
              v-if="dropdownOpen"
              class="dropdown user-menu-panel right-0 z-50 mt-2 w-72 origin-top-right py-0"
            >
              <div class="menu-section">
                <!--
                  The account card links to profile settings, indicated by its
                  trailing gear icon.
                -->
                <router-link to="/profile" @click="closeDropdown" class="user-menu-card">
                  <UserAvatar
                    :avatar-url="avatarUrl"
                    :user-id="user.id"
                    :alt="displayName"
                    size-class="h-9 w-9 shrink-0"
                  />
                  <span class="min-w-0 flex-1">
                    <span
                      class="block truncate text-sm font-medium text-primary-900 dark:text-dark-50"
                    >
                      {{ displayName }}
                    </span>
                    <span class="block truncate text-xs text-primary-900/60 dark:text-dark-400">
                      {{ user.email }}
                    </span>
                  </span>
                  <Icon
                    name="cog"
                    size="sm"
                    class="shrink-0 text-primary-900/45 dark:text-dark-400"
                  />
                </router-link>

                <!-- Below sm, show the balance beneath the account card. -->
                <div class="flex items-baseline justify-between gap-3 px-2.5 pb-1 pt-2 sm:hidden">
                  <span class="text-xs text-primary-900/60 dark:text-dark-400">{{
                    t('common.balance')
                  }}</span>
                  <span class="text-right">
                    <span
                      class="block text-sm font-semibold tabular-nums text-primary-900 dark:text-dark-50"
                    >
                      {{ formatHeaderMoney(availableBalance) }}
                    </span>
                    <span
                      v-if="frozenBalance > 0"
                      class="block text-xs text-amber-600 dark:text-amber-300"
                    >
                      {{ balanceFrozenLabel }}
                    </span>
                  </span>
                </div>
              </div>

              <!--
                Account navigation uses the same destination icons and public feature
                settings as the sidebar.
              -->
              <div v-for="group in menuNavGroups" :key="group.key" class="menu-section">
                <router-link
                  v-for="item in group.items"
                  :key="item.path"
                  :to="item.path"
                  @click="closeDropdown"
                  class="menu-item"
                >
                  <Icon :name="item.icon" size="md" class="shrink-0" />
                  {{ item.label }}
                </router-link>
              </div>

              <!-- Administrator actions expose the repository and guide replay. -->
              <div v-if="authStore.isAdmin" class="menu-section">
                <a
                  href="https://github.com/TokenFlux/TokenRouter"
                  target="_blank"
                  rel="noopener noreferrer"
                  @click="closeDropdown"
                  class="menu-item"
                >
                  <svg class="h-5 w-5 shrink-0" fill="currentColor" viewBox="0 0 24 24">
                    <path
                      fill-rule="evenodd"
                      clip-rule="evenodd"
                      d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.17 6.839 9.49.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.464-1.11-1.464-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0112 6.836c.85.004 1.705.114 2.504.336 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.167 22 16.418 22 12c0-5.523-4.477-10-10-10z"
                    />
                  </svg>
                  {{ t('nav.github') }}
                </a>

                <button
                  v-if="showOnboardingButton"
                  type="button"
                  @click="handleReplayGuide"
                  class="menu-item"
                >
                  <Icon name="questionCircle" size="md" class="shrink-0" />
                  {{ t('onboarding.restartTour') }}
                </button>
              </div>

              <div class="menu-section">
                <button type="button" @click="handleLogout" class="menu-item menu-item-danger">
                  <Icon name="logout" size="md" />
                  {{ t('nav.logout') }}
                </button>
              </div>

              <!-- Theme choices use the shared segmented control beneath the account menu. -->
              <div class="menu-section">
                <div
                  v-segmented
                  class="segmented grid grid-cols-3 gap-1"
                  role="radiogroup"
                  :aria-label="t('nav.theme')"
                >
                  <button
                    v-for="option in themeOptions"
                    :key="option.mode"
                    type="button"
                    role="radio"
                    :aria-checked="themeMode === option.mode"
                    :aria-label="option.label"
                    :title="option.label"
                    :data-testid="`theme-mode-${option.mode}`"
                    :class="[
                      'segmented-item flex items-center justify-center py-1.5',
                      themeMode === option.mode && 'segmented-item-active',
                    ]"
                    @click="setThemeMode(option.mode)"
                  >
                    <Icon :name="option.icon" size="sm" />
                  </button>
                </div>
              </div>
            </div>
          </MotionTransition>
        </div>

        <router-link v-else-if="publicPage" to="/login" class="btn btn-primary">
          {{ t('home.login') }}
        </router-link>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { vSegmented } from '@/directives/segmented'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import AnnouncementBell from '@/components/announcements/AnnouncementBell.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import HeaderContactSupport from '@/components/layout/HeaderContactSupport.vue'
import GlobalSearchTrigger from '@/components/layout/search/GlobalSearchTrigger.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons'
import VersionBadge from '@/components/admin/system/VersionBadge.vue'
import { sanitizeUrl } from '@/utils/url'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useTheme, type ThemeMode } from '@/composables/useTheme'

// AppHeader.vue renders the shared brand, the global search entry, labeled navigation
// entries, balance, and account menu. Marketplace, announcement, docs, support, and
// language entries show text from md upward so new users can read what each entry does;
// narrower headers fall back to icons. Authenticated users choose themes in the account
// menu, while guests keep a header theme toggle. Public pages add guest navigation,
// authenticated menus follow public feature settings, and the creative workspace
// provides a dashboard return link beside the brand. Menu listeners belong to the
// mounted header.

// publicPage hides the sidebar toggle and enables marketplace and guest login links
// while keeping the shared brand and account controls.
withDefaults(defineProps<{ publicPage?: boolean /* Public page navigation */ }>(), {
  publicPage: false,
})

// Router, stores, translation, balance formatting, and theme state supply the shared
// header actions and account display for this mounted component.
const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const { formatBalanceAmount, balanceUnitSymbol } = useBalanceDisplay()
const { isDark, themeMode, setThemeMode, toggleTheme } = useTheme()

// User and site values drive branding and account controls; public settings gate the
// logo, and dropdownOpen owns the local account-menu visibility.
const user = computed(() => authStore.user)
const isCreativeStudio = computed(() => route.path === '/creative')
const homePath = '/home'
const siteName = computed(() => appStore.siteName)
const siteLogo = computed(() =>
  sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }),
)
const siteVersion = computed(() => appStore.siteVersion)
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const dropdownOpen = ref(false)

// MenuNavItem describes a translated account-menu destination and its icon.
interface MenuNavItem {
  path: string // Router destination path
  label: string // Translated navigation label
  icon: IconName // Navigation icon name
}

// menuNavGroups places workspace entries before billing actions and removes entries
// disabled by the corresponding public feature settings.
const menuNavGroups = computed(() => {
  const settings = appStore.cachedPublicSettings
  const paymentEnabled = settings?.payment_enabled === true
  const groups: Array<{
    key: string /* Stable section identity */
    items: Array<MenuNavItem | false> /* Entries before filtering */
  }> = [
    {
      key: 'workspace',
      items: [
        { path: '/profile', label: t('nav.profile'), icon: 'userCircle' },
        { path: '/dashboard', label: t('nav.dashboard'), icon: 'dashboard' },
        { path: '/usage', label: t('nav.usage'), icon: 'chart' },
        { path: '/keys', label: t('nav.apiKeys'), icon: 'key' },
        settings?.team_enabled !== false && { path: '/team', label: t('nav.team'), icon: 'users' },
      ],
    },
    {
      key: 'billing',
      items: [
        paymentEnabled && { path: '/purchase', label: t('nav.buySubscription'), icon: 'recharge' },
        { path: '/subscriptions', label: t('nav.mySubscriptions'), icon: 'creditCard' },
        paymentEnabled && { path: '/orders', label: t('nav.myOrders'), icon: 'orderList' },
        { path: '/redeem', label: t('nav.redeem'), icon: 'gift' },
        settings?.affiliate_enabled === true && {
          path: '/affiliate',
          label: t('nav.affiliate'),
          icon: 'affiliate',
        },
      ],
    },
  ]
  return groups.map((group) => ({
    key: group.key,
    items: group.items.filter((item): item is MenuNavItem => Boolean(item)),
  }))
})

// themeOptions pairs each supported theme mode with its icon and translated accessible
// label, keeping the segmented menu bound to shared theme state.
const themeOptions = computed<
  Array<{
    mode: ThemeMode /* Requested theme mode */
    icon: IconName /* Mode indicator icon */
    label: string /* Translated accessible label */
  }>
>(() => [
  { mode: 'light', icon: 'sun', label: t('nav.lightMode') },
  { mode: 'dark', icon: 'moon', label: t('nav.darkMode') },
  { mode: 'system', icon: 'monitor', label: t('nav.systemTheme') },
])
// dropdownRef identifies the account-menu root for outside clicks; sanitized links and
// account balances below supply the menu's displayed account information.
const dropdownRef = ref<HTMLElement | null>(null)
const docUrl = computed(() => sanitizeUrl(appStore.docUrl))
const avatarUrl = computed(() => user.value?.avatar_url?.trim() || '')
const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
// balanceFrozenText uses a Chinese label when translation lookup returns the key; the
// complete label combines that text with the formatted frozen amount.
const balanceFrozenText = computed(() =>
  t('common.frozenBalance') === 'common.frozenBalance' ? '冻结金额' : t('common.frozenBalance'),
)
const balanceFrozenLabel = computed(
  () => `${balanceFrozenText.value} ${formatHeaderMoney(frozenBalance.value)}`,
)

// showOnboardingButton exposes guide replay when the current account has the
// administrator role, matching the surrounding administrator menu section.
const showOnboardingButton = computed(() => {
  return user.value?.role === 'admin'
})

// displayName prefers the account username, then the email local part; missing user
// identity produces an empty label for the avatar and account card.
const displayName = computed(() => {
  if (!user.value) return ''
  return user.value.username || user.value.email?.split('@')[0] || ''
})

// toggleDropdown toggles the local account-menu visibility for the avatar button.
function toggleDropdown() {
  dropdownOpen.value = !dropdownOpen.value
}

// closeDropdown closes the account menu after navigation or outside clicks.
function closeDropdown() {
  dropdownOpen.value = false
}

// handleLogout closes the menu, attempts logout, and navigates to login.
//
// A logout rejection is logged and still followed by navigation; navigation failures
// propagate to the event caller.
async function handleLogout() {
  closeDropdown()
  try {
    await authStore.logout()
  } catch (error) {
    // Continue to login after logging a logout failure so a rejected logout request does
    // not strand the account menu flow.
    console.error('Logout error:', error)
  }
  await router.push('/login')
}

// handleReplayGuide closes the menu before replaying the registered onboarding guide.
// The store owns guide availability and execution; this handler releases the menu
// so the guide can target page controls after it starts.
function handleReplayGuide() {
  closeDropdown()
  onboardingStore.replay()
}

// formatHeaderMoney formats finite balances with two fractional digits. Nonfinite input
// becomes zero; withSymbol defaults to true, while the balance button passes false
// because it lays out the currency symbol separately.
function formatHeaderMoney(value: number, withSymbol = true) {
  return formatBalanceAmount(Number.isFinite(value) ? value : 0, { fractionDigits: 2, withSymbol })
}

// handleClickOutside closes the account menu when a document click lands outside its
// root.
//
// A missing template reference leaves visibility unchanged, covering events before
// mounting or after the menu root disappears.
function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    closeDropdown()
  }
}

// Register the document click listener for this mounted header and remove the same
// callback before unmount so stale menus stop observing clicks.
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
/*
 * The right-hand group may shrink so a crowded header narrows the search entry, the
 * only child with a flexible width; navigation and account controls keep their size.
 */
.header-status-actions {
  @apply ml-auto flex min-w-0 items-center gap-2 sm:gap-5;
}

/*
 * Limit branding relative to the viewport so utility and account controls retain
 * room as the header becomes narrower.
 */
.header-brand {
  max-width: min(18rem, 42vw);
}

/*
 * Icon-sized entries below md sit 4px apart, widening to 8px from sm. From md the
 * entries' own horizontal padding separates the labels, so the gap returns to 4px.
 */
.header-nav-group {
  @apply flex items-center gap-1 sm:gap-2 md:gap-1;
}

.header-status-divider {
  @apply h-5 w-px shrink-0 bg-primary-900/10 dark:bg-dark-600;
}

.header-status-user-button {
  /*
   * The avatar uses a neutral ring and follows shared control hover colors, keeping
   * account identity separate from action emphasis.
   */
  @apply flex h-9 w-9 items-center justify-center rounded-full ring-1 ring-gray-200 transition-shadow hover:ring-black/20 dark:ring-dark-600 dark:hover:ring-dark-500;
}

/*
 * The menu reserves header height and one rem for its top gap and bottom clearance;
 * excess content scrolls inside the panel.
 */
.user-menu-panel {
  max-height: calc(100dvh - var(--header-h) - 1rem);
  @apply overflow-y-auto;
}

/*
 * The account card keeps a gray background matching the theme track, distinguishing
 * the profile entry from ordinary menu links.
 */
.user-menu-card {
  @apply flex items-center gap-3 rounded-control bg-gray-50 px-2.5 py-2 transition-colors hover:bg-gray-100;
  @apply dark:bg-dark-800 dark:hover:bg-dark-700;
}
</style>
