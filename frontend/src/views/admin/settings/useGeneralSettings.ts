import { ref, computed, watch } from 'vue'
import type { FooterLinkGroup } from '@/api/settings.types'
import { newContentID, originalContent, resolveContent } from '@/i18n/content'
import { getLocale } from '@/i18n'
import { useI18n } from 'vue-i18n'
import type { UsageRankingSortBy } from '@/api/admin/settings'
import type { MarketplaceGroup } from '@/api/marketplace.types'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { getMarketplaceModels } from '@/api/marketplace'
import type { SettingsForm } from './settingsState'
import { tablePageSizeMin, tablePageSizeMax } from './settingsDefaults'

/**
 * useGeneralSettings.ts edits site presentation, navigation rows, footer links, and
 * featured models in the shared settings draft. New localized rows receive stable content
 * IDs and source envelopes, while list moves preserve existing objects and menu ordering
 * is reflected in each row's sort_order. Normalizers prepare save values without replacing
 * the editable lists.
 *
 * Marketplace choices are separate from configured featured IDs: a failed lookup clears
 * the choices but leaves saved selections available. Pagination text is also separate from
 * its numeric draft so the save validator can reject incomplete input. Ranking changes
 * enable their selected metric, and balance previews resolve the current draft in the
 * active interface locale.
 */

/**
 * GeneralSettingsForm selects the shared site, navigation, and presentation fields used by
 * this controller.
 */
export type GeneralSettingsForm = Pick<
  SettingsForm,
  | 'api_base_url'
  | 'backend_mode_enabled'
  | 'balance_icon_svg'
  | 'balance_unit_symbol'
  | 'custom_endpoints'
  | 'custom_menu_items'
  | 'default_locale'
  | 'doc_url'
  | 'footer_links'
  | 'hide_ccs_import_button'
  | 'home_featured_models'
  | 'localized_settings'
  | 'marketplace_availability_bucket_minutes'
  | 'marketplace_availability_window_days'
  | 'reasoning_point_rmb_unit_price'
  | 'site_logo'
  | 'site_share_image'
  | 'site_texts'
  | 'table_default_page_size'
  | 'usage_ranking_enabled'
  | 'usage_ranking_limit'
  | 'usage_ranking_show_actual_cost'
  | 'usage_ranking_show_requests'
  | 'usage_ranking_show_total_tokens'
  | 'usage_ranking_sort_by'
  | 'usd_exchange_rate'
>

/**
 * useGeneralSettings binds editable lists, ranking visibility, and balance previews to the
 * supplied reactive settings object. Call it during component setup so its watcher and
 * i18n bindings use the page lifetime. Returned actions mutate the supplied draft, while
 * save normalizers return cleaned arrays for the bulk request.
 *
 * Keep the same form object for this controller and the general panel: its closures retain
 * that object. Marketplace loading changes candidate options independently from configured
 * model IDs, and pagination text requires the page hydration and validation steps to
 * synchronize its numeric representation.
 */
export function useGeneralSettings(form: GeneralSettingsForm) {
  // Translations follow the interface locale, which also selects balance-preview
  // content and the language of generated default footer labels.
  const { t, locale } = useI18n()

  // The page hydrates this text separately and validates it before updating
  // numeric page-size options in the shared settings draft.
  const tablePageSizeOptionsInput = ref('10, 20, 50, 100')

  // Sorting choices retain API values while their labels track the current
  // interface language used by the ranking controls.
  const usageRankingSortOptions = computed(() => [
    {
      value: 'total_tokens' as UsageRankingSortBy,
      label: t('admin.settings.usageRanking.sortOptions.totalTokens'),
    },
    {
      value: 'requests' as UsageRankingSortBy,
      label: t('admin.settings.usageRanking.sortOptions.requests'),
    },
    {
      value: 'actual_cost' as UsageRankingSortBy,
      label: t('admin.settings.usageRanking.sortOptions.actualCost'),
    },
  ])

  /**
   * ensureUsageRankingSortMetricVisible enables the display flag for the selected ranking
   * metric. It leaves other metric flags unchanged and treats an unrecognized sort value
   * as total tokens when normalizing the draft.
   */
  function ensureUsageRankingSortMetricVisible() {
    switch (form.usage_ranking_sort_by) {
      case 'requests':
        form.usage_ranking_show_requests = true
        break
      case 'actual_cost':
        form.usage_ranking_show_actual_cost = true
        break
      default:
        form.usage_ranking_show_total_tokens = true
        break
    }
  }

  // A newly selected metric must remain visible in the public ranking; the
  // panel also disables its toggle to prevent hiding the ordering basis.
  watch(
    () => form.usage_ranking_sort_by,
    () => ensureUsageRankingSortMetricVisible(),
  )

  // Preview formatting reads draft units and icons so unsaved display settings
  // can be inspected before the shared save action publishes them.
  const {
    balanceUnitName: previewBalanceUnitName,
    balanceUnitSymbol: previewBalanceUnitSymbol,
    formatBalanceAmount: formatPreviewBalanceAmount,
  } = useBalanceDisplay({
    unitName: computed(
      () => resolveContent(form.localized_settings.balance_unit_name, locale.value).value,
    ),
    unitSymbol: computed(() => form.balance_unit_symbol),
    iconSvg: computed(() => form.balance_icon_svg),
  })

  // A fixed fractional example exposes both unit placement and decimal formatting.
  const previewBalanceAmount = computed(() => formatPreviewBalanceAmount(123.45))

  // Menu audience choices use stored visibility values and localized labels
  // for the panel selector that controls which editor is displayed.
  const customMenuVisibilityOptions = computed(() => [
    { value: 'user', label: t('admin.settings.customMenu.visibilityUser') },
    { value: 'admin', label: t('admin.settings.customMenu.visibilityAdmin') },
  ])

  /**
   * addMenuItem appends a user-visible menu draft with a fresh content ID. Its source
   * locale is the current application locale, and its initial sort order follows existing
   * rows.
   */
  function addMenuItem() {
    form.custom_menu_items.push({
      id: newContentID(),
      localization: originalContent({ label: '', url: '' }, getLocale()),
      label: '',
      icon_svg: '',
      url: '',
      visibility: 'user',
      sort_order: form.custom_menu_items.length,
    })
  }

  /**
   * removeMenuItem removes the menu row at the caller's list index. Remaining row objects
   * keep their IDs and localization envelopes, while their sort_order fields are rewritten
   * to match array order.
   */
  function removeMenuItem(index: number) {
    form.custom_menu_items.splice(index, 1)
    // Menu consumers sort by sort_order, so row removal or movement must
    // publish matching positions alongside the updated draft array order.
    form.custom_menu_items.forEach((item, i) => {
      item.sort_order = i
    })
  }

  /**
   * moveMenuItem swaps a menu row with its adjacent destination and refreshes sort_order.
   * Callers pass a current row index; a destination outside the list leaves the draft
   * unchanged.
   */
  function moveMenuItem(index: number, direction: -1 | 1) {
    const targetIndex = index + direction
    if (targetIndex < 0 || targetIndex >= form.custom_menu_items.length) return
    const items = form.custom_menu_items
    const temp = items[index]
    items[index] = items[targetIndex]
    items[targetIndex] = temp
    // Menu consumers sort by sort_order, so row removal or movement must
    // publish matching positions alongside the updated draft array order.
    items.forEach((item, i) => {
      item.sort_order = i
    })
  }

  /**
   * addEndpoint appends an empty endpoint draft with a stable content ID. The current
   * locale owns its name and description source fields; its URL remains a separate
   * editable value.
   */
  function addEndpoint() {
    form.custom_endpoints.push({
      id: newContentID(),
      name: '',
      endpoint: '',
      description: '',
      localization: originalContent({ name: '', description: '' }, getLocale()),
    })
  }

  /**
   * removeEndpoint removes the custom endpoint at the supplied list index.
   */
  function removeEndpoint(index: number) {
    form.custom_endpoints.splice(index, 1)
  }

  /**
   * addFooterLink appends an empty localized link to the supplied footer group. The group
   * is the mutable draft row used by the panel, and the link receives a fresh content ID.
   */
  function addFooterLink(group: FooterLinkGroup) {
    group.links.push({
      id: newContentID(),
      label: '',
      url: '',
      localization: originalContent({ label: '', url: '' }, getLocale()),
    })
  }

  /**
   * addFooterGroup appends an empty footer group and localized title source. The group
   * receives a fresh content ID in the current locale and starts with an empty link array.
   */
  function addFooterGroup() {
    form.footer_links.push({
      id: newContentID(),
      title: '',
      localization: originalContent('', getLocale()),
      links: [],
    })
  }

  /**
   * applyDefaultFooterLinks replaces footer drafts with built-in page links and configured
   * developer URLs. It chooses Chinese labels for locales beginning with zh and English
   * labels otherwise, then drops groups containing no links.
   */
  function applyDefaultFooterLinks() {
    const zh = locale.value.toLowerCase().startsWith('zh')
    const groups: Array<{
      title: string // footer group heading
      // links contains destinations selected from configured URLs; groups with no
      // links are removed before this template replaces the editable footer.
      links: Array<{
        label: string // footer link text
        url: string // footer navigation destination
      }>
    }> = [
      {
        title: zh ? '产品' : 'Product',
        links: [
          { label: zh ? '模型广场' : 'Models', url: '/models' },
          { label: zh ? '用量查询' : 'Key usage', url: '/key-usage' },
          { label: zh ? '控制台' : 'Dashboard', url: '/dashboard' },
        ],
      },
      {
        title: zh ? '开发者' : 'Developer',
        links: [
          ...(form.doc_url ? [{ label: zh ? '文档' : 'Documentation', url: form.doc_url }] : []),
          ...(form.api_base_url ? [{ label: 'API Base URL', url: form.api_base_url }] : []),
        ],
      },
      {
        title: zh ? '支持' : 'Support',
        links: [{ label: zh ? '登录 / 注册' : 'Sign in / Sign up', url: '/login' }],
      },
    ]
    form.footer_links = groups.filter((g) => g.links.length > 0)
  }

  /**
   * removeFooterGroup removes the footer group at the supplied list index.
   */
  function removeFooterGroup(index: number) {
    form.footer_links.splice(index, 1)
  }

  /**
   * moveFooterGroup swaps a footer group with its adjacent destination in the shared
   * draft. Callers pass a current index; a destination outside the list leaves all groups
   * in place.
   */
  function moveFooterGroup(index: number, direction: -1 | 1) {
    const targetIndex = index + direction
    if (targetIndex < 0 || targetIndex >= form.footer_links.length) return
    const groups = form.footer_links
    const temp = groups[index]
    groups[index] = groups[targetIndex]
    groups[targetIndex] = temp
  }

  /**
   * normalizeFooterLinksForSave builds request groups with trimmed titles and complete
   * trimmed links. Each retained group requires a nonempty title and at least one link
   * whose label and URL are both nonempty after trimming. The editable group and link
   * records remain available in their existing order while the returned records carry
   * their IDs and other spread fields into the request.
   *
   * Localization envelopes remain shared references and their source strings are not
   * regenerated here. Callers use the copied plain fields for this cleanup and retain
   * the localized content prepared by the row editors.
   */
  function normalizeFooterLinksForSave() {
    return form.footer_links
      .map((group) => ({
        ...group,
        title: group.title.trim(),
        links: group.links
          .map((link) => ({ ...link, label: link.label.trim(), url: link.url.trim() }))
          .filter((link) => link.label && link.url),
      }))
      .filter((group) => group.title && group.links.length > 0)
  }

  // The row editor limits additions to the backend-supported featured-model count.
  const homeFeaturedModelsMax = 12

  // Public marketplace results supply grouped choices; configured IDs stay in
  // the shared draft even when these selectable options cannot be loaded.
  // value and label identify choices; kind and disabled distinguish headings
  // from model rows. Headings use synthetic values and cannot be selected.
  const homeFeaturedModelOptions = ref<
    Array<{
      value: string // model selection identifier
      label: string // model or group name
      kind?: string // group heading marker
      disabled?: boolean // heading selection disabled
    }>
  >([])

  /**
   * loadHomeFeaturedModelOptions replaces selectable models with the public marketplace
   * response grouped for display. A failed request clears available options but leaves
   * configured featured model IDs untouched for subsequent saving.
   */
  async function loadHomeFeaturedModelOptions() {
    try {
      const groups: MarketplaceGroup[] = await getMarketplaceModels()
      // value and label carry each choice; kind and disabled mark synthetic
      // group headers, which separate model choices while preventing header selection.
      const options: Array<{
        value: string // model selection identifier
        label: string // model or group name
        kind?: string // group heading marker
        disabled?: boolean // heading selection disabled
      }> = []
      for (const group of groups) {
        if (!group.models?.length) continue
        options.push({
          value: `__group_${group.id}`,
          label: group.name,
          kind: 'group',
          disabled: true,
        })
        for (const model of group.models) {
          options.push({
            value: model.id,
            label: model.id,
          })
        }
      }
      homeFeaturedModelOptions.value = options
    } catch {
      // Marketplace failure removes suggestions while leaving configured IDs in
      // the draft, so unrelated settings and existing selections remain saveable.
      homeFeaturedModelOptions.value = []
    }
  }

  /**
   * removeHomeFeaturedModel removes the featured model at the supplied list index.
   */
  function removeHomeFeaturedModel(index: number) {
    form.home_featured_models.splice(index, 1)
  }

  /**
   * moveHomeFeaturedModel swaps adjacent configured IDs to change homepage display order.
   * Callers pass a current index; a destination outside the list leaves the configured
   * order unchanged for saving.
   */
  function moveHomeFeaturedModel(index: number, direction: -1 | 1) {
    const targetIndex = index + direction
    if (targetIndex < 0 || targetIndex >= form.home_featured_models.length) return
    const models = form.home_featured_models
    const temp = models[index]
    models[index] = models[targetIndex]
    models[targetIndex] = temp
  }

  /**
   * normalizeHomeFeaturedModelsForSave returns trimmed, nonempty featured IDs in
   * administrator order. Repeated IDs keep their first occurrence, and the editable array
   * remains available with its existing rows until persistence completes.
   */
  function normalizeHomeFeaturedModelsForSave() {
    const seen = new Set<string>()
    return form.home_featured_models
      .map((id) => id.trim())
      .filter((id) => {
        if (!id || seen.has(id)) return false
        seen.add(id)
        return true
      })
  }

  /**
   * formatTablePageSizeOptions joins numeric sizes into the comma-separated input display.
   */
  function formatTablePageSizeOptions(options: number[]): string {
    return options.join(', ')
  }

  /**
   * parseTablePageSizeOptionsInput parses comma-separated whole page sizes within
   * configured bounds. Empty input, fractional values, or out-of-range sizes return null;
   * valid sizes are deduplicated and sorted numerically for validation and saving.
   */
  function parseTablePageSizeOptionsInput(raw: string): number[] | null {
    const tokens = raw
      .split(',')
      .map((token) => token.trim())
      .filter((token) => token.length > 0)

    if (tokens.length === 0) {
      return null
    }

    const parsed = tokens.map((token) => Number(token))
    if (parsed.some((value) => !Number.isInteger(value))) {
      return null
    }

    const deduped = Array.from(new Set(parsed)).sort((a, b) => a - b)
    if (deduped.some((value) => value < tablePageSizeMin || value > tablePageSizeMax)) {
      return null
    }

    return deduped
  }
  return {
    tablePageSizeOptionsInput,
    usageRankingSortOptions,
    ensureUsageRankingSortMetricVisible,
    previewBalanceUnitName,
    previewBalanceUnitSymbol,
    formatPreviewBalanceAmount,
    previewBalanceAmount,
    customMenuVisibilityOptions,
    addMenuItem,
    removeMenuItem,
    moveMenuItem,
    addEndpoint,
    removeEndpoint,
    addFooterLink,
    addFooterGroup,
    applyDefaultFooterLinks,
    removeFooterGroup,
    moveFooterGroup,
    normalizeFooterLinksForSave,
    homeFeaturedModelsMax,
    homeFeaturedModelOptions,
    loadHomeFeaturedModelOptions,
    removeHomeFeaturedModel,
    moveHomeFeaturedModel,
    normalizeHomeFeaturedModelsForSave,
    formatTablePageSizeOptions,
    parseTablePageSizeOptionsInput,
  }
}
