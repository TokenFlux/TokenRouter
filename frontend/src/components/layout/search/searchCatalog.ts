import { computed, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { IconName } from '@/components/icons/registry'
import { useNavigationItems, type NavItem } from '@/components/layout/navigationItems'
import { useClipboard } from '@/composables/useClipboard'
import { useTheme } from '@/composables/useTheme'
import { availableLocales, setLocale } from '@/i18n'
import { useAppStore, useAuthStore } from '@/stores'
import { bestMatch, type FieldMatch } from '@/utils/fuzzyMatch'
import type { ModelSearchEntry } from '@/utils/marketplaceCatalog'
import { sanitizeUrl } from '@/utils/url'

// searchCatalog.ts defines what the global search palette can find and how matches are
// ranked. It turns the shared console navigation, the guest pages, a few account actions,
// and the model index into one item list per kind, filtered by the same role, feature,
// and backend-mode rules the sidebar and header apply.
//
// Every item has a stable key ("model:<id>", "page:<path>", "action:<name>") so recent
// visits can be stored as keys and resolved against the current lists later; an item the
// account can no longer see simply does not resolve. Hidden keywords make pages and
// actions findable in either language: a Chinese interface still finds API Keys for
// "key", and the route path is always a keyword, so "usage" finds the usage page by URL.
//
// rankItems scores each item's visible fields before its hidden keywords. Earlier fields
// win score ties, and the palette highlights only the title or model ID when one of them
// produced the best match.

// SearchKind names the result sections shown in the palette.
export type SearchKind = 'model' | 'page' | 'action'

// SearchItemBase holds the fields every result kind shares for matching and identity.
interface SearchItemBase {
  key: string // Stable identity, also stored for recent visits.
  title: string // Visible primary text, highlighted on match.
  keywords: readonly string[] // Hidden match text and aliases.
}

// ModelSearchItem is a marketplace model; selecting it opens the filtered marketplace.
export interface ModelSearchItem extends SearchItemBase {
  kind: 'model'
  model: ModelSearchEntry // Merged catalog entry.
}

// PageSearchItem is a navigable destination; external pages open in a new tab.
export interface PageSearchItem extends SearchItemBase {
  kind: 'page'
  to: string // Router path or external URL.
  external: boolean // Opens outside the application.
  area: string // Section label, empty when unambiguous.
  icon: IconName | null // Registered icon, or null for iconSvg.
  iconSvg?: string // Unsanitized uploaded SVG markup.
}

// ActionSearchItem runs an operation such as switching the theme or signing out.
export interface ActionSearchItem extends SearchItemBase {
  kind: 'action'
  icon: IconName // Registered action icon.
  run: () => void | Promise<unknown> // Performs the action.
}

// SearchItem is any result the palette can list and select.
export type SearchItem = ModelSearchItem | PageSearchItem | ActionSearchItem

// RankedItem pairs an item with its best match; match is null for unranked listings
// such as recent visits and suggestions shown before the user types.
export interface RankedItem {
  item: SearchItem // Matched or suggested item.
  match: FieldMatch | null // Best field match, if ranked.
}

// PAGE_KEYWORDS adds bilingual aliases to destinations whose labels do not contain the
// words people commonly search for. Paths not listed rely on their label and path.
const PAGE_KEYWORDS: Record<string, readonly string[]> = {
  '/dashboard': ['home', 'overview', '首页', '概览'],
  '/models': ['marketplace', 'price', 'pricing', '模型', '价格'],
  '/usage-ranking': ['leaderboard', '排行', '排名'],
  '/keys': ['key', 'token', 'api key', '密钥', '令牌'],
  '/team': ['members', '团队', '成员'],
  '/creative': ['image', 'studio', '创作', '绘图', '图片'],
  '/usage': ['logs', 'records', '用量', '日志', '记录'],
  '/subscriptions': ['plan', '订阅', '套餐'],
  '/purchase': ['topup', 'top up', 'recharge', 'buy', 'pay', '充值', '购买', '支付'],
  '/orders': ['order', '订单'],
  '/redeem': ['code', '兑换', '兑换码'],
  '/affiliate': ['invite', 'referral', '邀请', '返利', '推广'],
  '/profile': ['account', 'password', '账户', '资料', '密码'],
  '/admin/ops': ['monitor', '运维', '监控'],
  '/admin/users': ['user', '用户'],
  '/admin/groups': ['group', '分组'],
  '/admin/pricing': ['price', '价格', '定价'],
  '/admin/model-attributes': ['attribute', '属性'],
  '/admin/providers': ['channel', 'upstream', '渠道', '提供商', '上游'],
  '/admin/announcements': ['notice', '公告'],
  '/admin/proxies': ['proxy', 'ip', '代理'],
  '/admin/risk-control': ['risk', '风控'],
  '/admin/promo-codes': ['coupon', '优惠码', '优惠券'],
  '/admin/requests': ['request', '请求'],
  '/admin/audit-logs': ['audit', '审计', '日志'],
  '/admin/plugins': ['extension', '插件'],
  '/admin/settings': ['config', 'settings', '设置', '配置'],
  '/login': ['sign in', 'login', '登录'],
  '/register': ['sign up', 'register', '注册'],
}

// ACTION_KEYWORDS adds bilingual aliases to each action, keyed by the action name.
const ACTION_KEYWORDS: Record<string, readonly string[]> = {
  createKey: ['new key', 'create key', 'token', '新建', '创建', '密钥'],
  theme: ['theme', 'dark', 'light', '主题', '深色', '浅色'],
  language: ['language', 'locale', '语言'],
  copyApiUrl: ['api', 'base url', 'endpoint', '接口', '地址', '复制'],
  logout: ['logout', 'sign out', '退出', '登出'],
}

// searchFields lists an item's match fields with visible text first, so bestMatch
// reports field 0 for the title and, for models, field 1 for the model ID.
function searchFields(item: SearchItem): string[] {
  if (item.kind === 'model') {
    return [item.title, item.model.id, ...item.keywords]
  }
  return [item.title, ...item.keywords]
}

// rankItems returns the items that match the query, best first. Ties keep recently
// visited items ahead, in the order of recentKeys, and then prefer shorter titles, so
// "gpt-4o" ranks before "gpt-4o-mini" for the query "gpt-4o". Items that do not match
// are dropped; an empty query therefore returns an empty list.
export function rankItems(
  query: string,
  items: readonly SearchItem[],
  recentKeys: readonly string[],
): RankedItem[] {
  const recentRank = (key: string) => {
    const index = recentKeys.indexOf(key)
    return index === -1 ? recentKeys.length : index
  }
  const ranked: RankedItem[] = []
  for (const item of items) {
    const match = bestMatch(query, searchFields(item))
    if (match) ranked.push({ item, match })
  }
  return ranked.sort(
    (left, right) =>
      right.match!.score - left.match!.score ||
      recentRank(left.item.key) - recentRank(right.item.key) ||
      left.item.title.length - right.item.title.length,
  )
}

// SearchCatalog exposes the role-filtered item lists the palette searches.
export interface SearchCatalog {
  models: ComputedRef<ModelSearchItem[]> // One item per catalog model.
  pages: ComputedRef<PageSearchItem[]> // Visible destinations.
  actions: ComputedRef<ActionSearchItem[]> // Available account actions.
}

// useSearchCatalog builds the palette's item lists from the model index and the current
// account. Call it during component setup. Lists follow the sidebar's role rules:
// administrators get management pages plus their personal section, ordinary users get
// the account pages unless backend mode hides them, and guests get public pages only.
//
// Administrator-only entries depend on administrator settings that the caller must
// fetch. Action handlers navigate or change shared state directly; the palette closes
// itself before running them.
export function useSearchCatalog(modelEntries: Ref<readonly ModelSearchEntry[]>): SearchCatalog {
  const { t, locale } = useI18n()
  const router = useRouter()
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const { isDark, toggleTheme } = useTheme()
  const { copyToClipboard } = useClipboard()
  const { accountNavItems, adminNavItems } = useNavigationItems()

  // Account visibility mirrors AppSidebar: backend mode hides the personal console from
  // ordinary users but not from administrators.
  const isAuthenticated = computed(() => authStore.isAuthenticated)
  const hasAccountPages = computed(
    () => isAuthenticated.value && (authStore.isAdmin || !appStore.backendModeEnabled),
  )
  const docUrl = computed(() => sanitizeUrl(appStore.docUrl))

  // pageItem converts one navigation entry into a page item. Group children are
  // titled "Parent › Child" so identically named children stay distinguishable.
  function pageItem(item: NavItem, area: string, parent?: NavItem): PageSearchItem {
    return {
      kind: 'page',
      key: `page:${item.path}`,
      title: parent ? `${parent.label} › ${item.label}` : item.label,
      keywords: [item.path, ...(PAGE_KEYWORDS[item.path] ?? [])],
      to: item.path,
      external: false,
      area,
      icon: item.icon,
      iconSvg: item.iconSvg,
    }
  }

  // flattenNav expands groups into their child destinations; a group key is not itself
  // a page, so only children become items.
  function flattenNav(items: readonly NavItem[], area: string): PageSearchItem[] {
    return items.flatMap((item) =>
      item.children?.length
        ? item.children.map((child) => pageItem(child, area, item))
        : [pageItem(item, area)],
    )
  }

  // guestPages lists the destinations a signed-out visitor can open from the headers.
  function guestPages(): PageSearchItem[] {
    const entries: Array<[string, string, IconName]> = [
      ['/models', t('nav.modelMarketplace'), 'modelMarketplace'],
      ['/login', t('globalSearch.pages.login'), 'login'],
    ]
    if (appStore.cachedPublicSettings?.registration_enabled !== false) {
      entries.push(['/register', t('globalSearch.pages.register'), 'userPlus'])
    }
    return entries.map(([path, label, icon]) => pageItem({ path, label, icon }, ''))
  }

  // models wraps each index entry; brands and attribute terms become hidden keywords so
  // "anthropic" or a configured alias finds a model whose name does not contain it.
  const models = computed<ModelSearchItem[]>(() =>
    modelEntries.value.map((model) => ({
      kind: 'model',
      key: `model:${model.id}`,
      title: model.displayName,
      keywords: [...model.brands, ...model.searchTerms],
      model,
    })),
  )

  // pages orders management destinations before the personal section for administrators.
  // A management page and a personal page can share a label (Dashboard, Usage), so the
  // area label tells them apart; ordinary users see one section and need no label. A
  // custom or plugin menu that repeats a path keeps only the first entry, because result
  // keys must be unique within a list.
  const pages = computed<PageSearchItem[]>(() => {
    const items: PageSearchItem[] = []
    if (!isAuthenticated.value) {
      items.push(...guestPages())
    } else if (authStore.isAdmin) {
      items.push(...flattenNav(adminNavItems.value, t('globalSearch.areas.admin')))
      items.push(...flattenNav(accountNavItems.value, t('nav.myAccount')))
    } else if (hasAccountPages.value) {
      items.push(...flattenNav(accountNavItems.value, ''))
    }
    if (docUrl.value) {
      items.push({
        kind: 'page',
        key: 'page:docs',
        title: t('nav.docs'),
        keywords: ['docs', 'documentation', 'help', '文档', '帮助'],
        to: docUrl.value,
        external: true,
        area: '',
        icon: 'book',
      })
    }
    const seen = new Set<string>()
    return items.filter((item) => {
      if (seen.has(item.key)) return false
      seen.add(item.key)
      return true
    })
  })

  // copyApiUrl copies the advertised API base URL, falling back to this site's origin as
  // the keys page does when no public base URL is configured.
  function copyApiUrl() {
    const baseUrl = appStore.cachedPublicSettings?.api_base_url || window.location.origin
    void copyToClipboard(baseUrl)
  }

  // logout signs out and returns to the login page. A rejected logout request is logged
  // and still followed by navigation, matching the header account menu.
  async function logout() {
    try {
      await authStore.logout()
    } catch (error) {
      console.error('Logout error:', error)
    }
    await router.push('/login')
  }

  // actions lists account operations in display order. Creating a key needs the account
  // pages, signing out needs a session, and switching language offers every locale
  // other than the current one; theme and API address actions are always available.
  const actions = computed<ActionSearchItem[]>(() => {
    const items: ActionSearchItem[] = []
    const action = (name: string, title: string, icon: IconName, run: ActionSearchItem['run']) =>
      items.push({
        kind: 'action',
        key: `action:${name}`,
        title,
        keywords: ACTION_KEYWORDS[name] ?? [],
        icon,
        run,
      })

    if (hasAccountPages.value) {
      action('createKey', t('globalSearch.actions.createKey'), 'plus', () =>
        router.push({ path: '/keys', query: { create: '1' } }),
      )
    }
    action(
      'theme',
      t('globalSearch.actions.switchTheme', {
        mode: isDark.value ? t('nav.lightMode') : t('nav.darkMode'),
      }),
      isDark.value ? 'sun' : 'moon',
      toggleTheme,
    )
    for (const option of availableLocales) {
      if (option.code === locale.value) continue
      items.push({
        kind: 'action',
        key: `action:language:${option.code}`,
        title: t('globalSearch.actions.switchLanguage', { name: option.name }),
        keywords: [...ACTION_KEYWORDS.language, option.name, option.code],
        icon: 'languages',
        run: () => setLocale(option.code),
      })
    }
    action('copyApiUrl', t('globalSearch.actions.copyApiUrl'), 'copy', copyApiUrl)
    if (isAuthenticated.value) {
      action('logout', t('nav.logout'), 'logout', logout)
    }
    return items
  })

  return { models, pages, actions }
}
