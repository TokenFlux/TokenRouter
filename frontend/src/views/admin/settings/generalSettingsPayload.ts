import type { SettingsForm } from './settingsState'
import type { UpdateSettingsRequest } from '@/api/admin/settings'
import type { useGeneralSettings } from './useGeneralSettings'

/**
 * generalSettingsPayload.ts builds the site presentation and navigation fields of the bulk
 * settings request. The coordinator supplies a validated shared draft; footer groups and
 * featured IDs pass through the general controller's normalizers, while other collections
 * and localized content retain their draft references.
 */

/**
 * buildGeneralSettingsPayload returns site, display, and navigation fields after
 * coordinator validation. Pass actions bound to the same form; footer and featured-model
 * arrays are normalized, while other collections retain their existing shared references.
 */
export function buildGeneralSettingsPayload(
  form: Pick<
    SettingsForm,
    | 'balance_unit_symbol'
    | 'balance_icon_svg'
    | 'reasoning_point_rmb_unit_price'
    | 'usd_exchange_rate'
    | 'marketplace_availability_window_days'
    | 'marketplace_availability_bucket_minutes'
    | 'localized_settings'
    | 'site_texts'
    | 'default_locale'
    | 'site_logo'
    | 'site_share_image'
    | 'api_base_url'
    | 'backend_mode_enabled'
    | 'hide_ccs_import_button'
    | 'table_default_page_size'
    | 'table_page_size_options'
    | 'usage_ranking_limit'
    | 'usage_ranking_enabled'
    | 'usage_ranking_sort_by'
    | 'usage_ranking_show_total_tokens'
    | 'usage_ranking_show_requests'
    | 'usage_ranking_show_actual_cost'
    | 'custom_menu_items'
    | 'custom_endpoints'
    | 'frontend_url'
  >,
  actions: Pick<
    ReturnType<typeof useGeneralSettings>,
    'normalizeFooterLinksForSave' | 'normalizeHomeFeaturedModelsForSave'
  >,
): Pick<
  UpdateSettingsRequest,
  | 'balance_unit_symbol'
  | 'balance_icon_svg'
  | 'reasoning_point_rmb_unit_price'
  | 'usd_exchange_rate'
  | 'marketplace_availability_window_days'
  | 'marketplace_availability_bucket_minutes'
  | 'localized_settings'
  | 'site_texts'
  | 'default_locale'
  | 'site_logo'
  | 'site_share_image'
  | 'api_base_url'
  | 'backend_mode_enabled'
  | 'hide_ccs_import_button'
  | 'table_default_page_size'
  | 'table_page_size_options'
  | 'usage_ranking_limit'
  | 'usage_ranking_enabled'
  | 'usage_ranking_sort_by'
  | 'usage_ranking_show_total_tokens'
  | 'usage_ranking_show_requests'
  | 'usage_ranking_show_actual_cost'
  | 'custom_menu_items'
  | 'custom_endpoints'
  | 'footer_links'
  | 'home_featured_models'
  | 'frontend_url'
> {
  const { normalizeFooterLinksForSave, normalizeHomeFeaturedModelsForSave } = actions
  return {
    balance_unit_symbol: form.balance_unit_symbol,

    balance_icon_svg: form.balance_icon_svg,

    reasoning_point_rmb_unit_price: form.reasoning_point_rmb_unit_price,

    usd_exchange_rate: form.usd_exchange_rate,

    marketplace_availability_window_days: form.marketplace_availability_window_days,

    marketplace_availability_bucket_minutes: form.marketplace_availability_bucket_minutes,

    localized_settings: form.localized_settings,

    site_texts: form.site_texts,

    default_locale: form.default_locale,

    site_logo: form.site_logo,
    site_share_image: form.site_share_image,

    api_base_url: form.api_base_url,

    backend_mode_enabled: form.backend_mode_enabled,

    hide_ccs_import_button: form.hide_ccs_import_button,

    table_default_page_size: form.table_default_page_size,

    table_page_size_options: form.table_page_size_options,

    usage_ranking_limit: form.usage_ranking_limit,

    usage_ranking_enabled: form.usage_ranking_enabled,

    usage_ranking_sort_by: form.usage_ranking_sort_by,

    usage_ranking_show_total_tokens: form.usage_ranking_show_total_tokens,

    usage_ranking_show_requests: form.usage_ranking_show_requests,

    usage_ranking_show_actual_cost: form.usage_ranking_show_actual_cost,

    custom_menu_items: form.custom_menu_items,

    custom_endpoints: form.custom_endpoints,

    footer_links: normalizeFooterLinksForSave(),

    home_featured_models: normalizeHomeFeaturedModelsForSave(),

    frontend_url: form.frontend_url,
  }
}
