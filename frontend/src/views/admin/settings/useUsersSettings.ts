import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { buildAuthSourceDefaultsState } from './settingsFormHelpers'
import type {
  AuthSourceDefaultsState,
  AuthSourceType,
  DefaultSubscriptionSetting,
} from '@/api/admin/settings.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import type { SettingsForm } from './settingsState'

/**
 * useUsersSettings.ts manages subscription plan choices and signup grants for the
 * user-defaults settings panel. Global subscriptions live in the shared bulk draft,
 * while each authentication source has its own reactive grant settings. The page
 * loads plans separately and validates duplicate selections before constructing its
 * save request.
 */

/**
 * UsersSettingsForm selects global signup defaults edited by the user settings
 * panel.
 */
export type UsersSettingsForm = Pick<
  SettingsForm,
  | 'default_balance'
  | 'default_concurrency'
  | 'default_subscriptions'
  | 'default_user_api_key_limit'
  | 'default_user_rpm_limit'
  | 'force_email_on_third_party_signup'
>

/**
 * DefaultSubscriptionPlanOption represents one loaded subscription plan in the
 * shared Select component.
 */
export interface DefaultSubscriptionPlanOption {
  value: number // subscription plan identifier
  label: string // displayed plan name
  description: string | null // optional plan description
  [key: string]: unknown // additional selector metadata
}
/**
 * useUsersSettings creates plan choices and per-source grant drafts during component
 * setup. The page loads subscription plans separately, hydrates authSourceDefaults
 * from server settings, and includes those shared objects in its bulk dirty
 * snapshot.
 */
export function useUsersSettings(form: UsersSettingsForm) {
  /**
   * t translates authentication-source names and descriptions when the settings
   * panel renders its grant controls.
   */
  const { t } = useI18n()

  /**
   * subscriptionPlans holds the loaded plan catalog used by dropdown options and
   * next-available subscription insertion actions.
   */
  const subscriptionPlans = ref<SubscriptionPlan[]>([])

  /**
   * authSourceDefaults owns mutable grant settings per authentication source,
   * initialized by the API helper and later hydrated by the page.
   */
  const authSourceDefaults = reactive<AuthSourceDefaultsState>(buildAuthSourceDefaultsState({}))

  /**
   * authSourceDefaultsMeta defines the display and validation order for seven
   * sources, with labels resolved from the current locale.
   */
  const authSourceDefaultsMeta = computed(() => [
    {
      source: 'email' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.email.title'),
      description: t('admin.settings.authSourceDefaults.sources.email.description'),
    },
    {
      source: 'linuxdo' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.linuxdo.title'),
      description: t('admin.settings.authSourceDefaults.sources.linuxdo.description'),
    },
    {
      source: 'oidc' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.oidc.title'),
      description: t('admin.settings.authSourceDefaults.sources.oidc.description'),
    },
    {
      source: 'wechat' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.wechat.title'),
      description: t('admin.settings.authSourceDefaults.sources.wechat.description'),
    },
    {
      source: 'github' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.github.title'),
      description: t('admin.settings.authSourceDefaults.sources.github.description'),
    },
    {
      source: 'google' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.google.title'),
      description: t('admin.settings.authSourceDefaults.sources.google.description'),
    },
    {
      source: 'dingtalk' as AuthSourceType,
      title: t('admin.settings.authSourceDefaults.sources.dingtalk.title'),
      description: t('admin.settings.authSourceDefaults.sources.dingtalk.description'),
    },
  ])

  /**
   * defaultSubscriptionPlanOptions maps the loaded catalog into selector values
   * while retaining optional descriptions for each available plan.
   */
  const defaultSubscriptionPlanOptions = computed<DefaultSubscriptionPlanOption[]>(() =>
    subscriptionPlans.value.map((plan) => ({
      value: plan.id,
      label: plan.name,
      description: plan.description,
    })),
  )

  /**
   * loadSubscriptionPlans replaces the available plan catalog from the administrator
   * payment API. Missing response data or a failed request produces an empty
   * catalog, which disables adding subscriptions until plans are loaded.
   */
  async function loadSubscriptionPlans() {
    try {
      const response = await adminAPI.payment.getPlans()
      // An absent catalog is represented as an empty list, so the panel cannot add a
      // plan with no loaded choice.
      subscriptionPlans.value = response.data || []
    } catch (_error: unknown) {
      // A failed refresh removes stale choices from the catalog; existing
      // subscription rows stay in their separate editable drafts.
      subscriptionPlans.value = []
    }
  }

  /**
   * findNextAvailableSubscriptionPlan returns the first loaded plan whose identifier
   * is absent from the supplied list. It returns undefined when every loaded plan is
   * already selected, allowing add actions to leave the draft unchanged.
   */
  function findNextAvailableSubscriptionPlan(
    existingPlanIDs: number[],
  ): SubscriptionPlan | undefined {
    const existing = new Set(existingPlanIDs)
    return subscriptionPlans.value.find((plan) => !existing.has(plan.id))
  }

  /**
   * addDefaultSubscription appends the first unselected loaded plan to global signup
   * subscriptions. It leaves the draft unchanged when the catalog is empty or every
   * loaded plan already appears in the list.
   */
  function addDefaultSubscription() {
    if (subscriptionPlans.value.length === 0) return
    const candidate = findNextAvailableSubscriptionPlan(
      form.default_subscriptions.map((item) => item.plan_id),
    )
    if (!candidate) return
    form.default_subscriptions.push({
      plan_id: candidate.id,
    })
  }

  /**
   * removeDefaultSubscription removes a global signup subscription at the supplied
   * array index. The panel passes an existing row index from RuleListEditor; this
   * helper uses array splice behavior directly.
   */
  function removeDefaultSubscription(index: number) {
    form.default_subscriptions.splice(index, 1)
  }

  /**
   * addAuthSourceDefaultSubscription appends the first unselected plan for one
   * authentication source. Selection is independent for each source; an empty
   * catalog or exhausted source list leaves its subscriptions unchanged.
   */
  function addAuthSourceDefaultSubscription(source: AuthSourceType) {
    if (subscriptionPlans.value.length === 0) return
    const candidate = findNextAvailableSubscriptionPlan(
      authSourceDefaults[source].subscriptions.map((item) => item.plan_id),
    )
    if (!candidate) return
    authSourceDefaults[source].subscriptions.push({
      plan_id: candidate.id,
    })
  }

  /**
   * removeAuthSourceDefaultSubscription removes the indexed subscription from the
   * selected authentication source. The panel supplies an existing row index from
   * that source editor, and the shared source draft is mutated directly.
   */
  function removeAuthSourceDefaultSubscription(source: AuthSourceType, index: number) {
    authSourceDefaults[source].subscriptions.splice(index, 1)
  }

  /**
   * findDuplicateDefaultSubscription returns the first subscription whose plan
   * identifier was already encountered. It returns undefined for unique lists and
   * leaves the supplied array unchanged, allowing validation to report the duplicate
   * plan.
   */
  function findDuplicateDefaultSubscription(
    subscriptions: DefaultSubscriptionSetting[],
  ): DefaultSubscriptionSetting | undefined {
    const seenPlanIDs = new Set<number>()

    return subscriptions.find((item) => {
      if (seenPlanIDs.has(item.plan_id)) {
        return true
      }
      seenPlanIDs.add(item.plan_id)
      return false
    })
  }
  /**
   * The returned plan catalog and grant drafts are shared by the panel, hydration
   * routine, and ordered bulk-save validation.
   */
  return {
    subscriptionPlans,
    authSourceDefaults,
    authSourceDefaultsMeta,
    defaultSubscriptionPlanOptions,
    loadSubscriptionPlans,
    findNextAvailableSubscriptionPlan,
    addDefaultSubscription,
    removeDefaultSubscription,
    addAuthSourceDefaultSubscription,
    removeAuthSourceDefaultSubscription,
    findDuplicateDefaultSubscription,
  }
}
