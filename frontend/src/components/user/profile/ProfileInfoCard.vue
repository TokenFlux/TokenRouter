<template>
  <div class="space-y-4">
    <section
      data-testid="profile-overview-hero"
      class="card overflow-hidden border border-primary-100/80 bg-gradient-to-br from-primary-50 via-white to-amber-50/70 dark:border-primary-900/40 dark:from-primary-950/40 dark:via-dark-900 dark:to-dark-950"
    >
      <div class="px-6 py-6 md:px-8">
        <div class="flex flex-col gap-6 lg:flex-row lg:items-start">
          <UserAvatar
            :avatar-url="avatarUrl"
            :user-id="user?.id"
            :alt="displayName"
            size-class="h-20 w-20 shrink-0"
          />

          <div class="min-w-0 flex-1 space-y-5">
            <div class="space-y-3">
              <div class="flex flex-wrap items-center gap-2">
                <h2 class="truncate text-2xl font-semibold text-gray-900 dark:text-white">
                  {{ displayName }}
                </h2>
                <UserIdBadge :user-id="user?.id" />
                <span :class="['badge', user?.role === 'admin' ? 'badge-primary' : 'badge-gray']">
                  {{ user?.role === 'admin' ? t('profile.administrator') : t('profile.user') }}
                </span>
                <span
                  :class="['badge', user?.status === 'active' ? 'badge-success' : 'badge-danger']"
                >
                  {{ user?.status === 'active' ? t('common.active') : t('common.disabled') }}
                </span>
              </div>

              <div class="space-y-1">
                <p class="truncate text-sm text-gray-600 dark:text-gray-300">
                  {{ primaryEmailDisplay }}
                </p>
                <div
                  v-if="sourceHints.length"
                  class="flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400"
                >
                  <span
                    v-for="hint in sourceHints"
                    :key="hint.key"
                    class="inline-flex items-center gap-1 rounded-full bg-white/80 px-3 py-1 ring-1 ring-primary-100 dark:bg-dark-900/70 dark:ring-primary-900/40"
                  >
                    <Icon name="link" size="sm" />
                    {{ hint.text }}
                  </span>
                </div>
              </div>
            </div>

            <div class="flex flex-wrap gap-x-10 gap-y-4">
              <div data-testid="profile-overview-metric-balance">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('profile.accountBalance') }}
                </p>
                <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">
                  {{ formatBalanceAmount(user?.balance ?? 0) }}
                </p>
              </div>
              <div data-testid="profile-overview-metric-concurrency">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('profile.concurrencyLimit') }}
                </p>
                <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">
                  {{ user?.concurrency || 0 }}
                </p>
              </div>
              <div data-testid="profile-overview-metric-member-since">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('profile.memberSince') }}
                </p>
                <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">
                  {{ memberSinceLabel }}
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <!-- The wide profile layout places account details beside the security and notification sections supplied by the page. -->
    <div class="grid grid-cols-1 gap-4 xl:grid-cols-2 xl:items-start">
      <div data-testid="profile-main-column" class="min-w-0 space-y-4">
        <section data-testid="profile-basics-panel" class="card">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <h2 class="text-lg font-medium text-gray-900 dark:text-white">
              {{ t('profile.basicsTitle') }}
            </h2>
          </div>

          <!-- Within the two-column layout, limited column width stacks avatar and username controls again. -->
          <div
            class="flex flex-col gap-6 px-6 py-6 md:flex-row md:items-center md:gap-8 xl:flex-col xl:items-stretch xl:gap-6"
          >
            <ProfileAvatarCard :user="user" embedded class="shrink-0" />

            <!-- The separator follows the layout, becoming horizontal for stacked controls and vertical for adjacent controls. -->
            <div
              class="h-px w-full bg-gray-100 dark:bg-dark-700 md:h-auto md:w-px md:self-stretch xl:h-px xl:w-full xl:self-auto"
            />

            <ProfileEditForm
              :initial-username="user?.username || ''"
              embedded
              class="min-w-0 flex-1"
            />
          </div>
        </section>

        <!-- Standalone card framing keeps this heading consistent with the security cards in the adjacent column. -->
        <ProfileIdentityBindingsSection
          data-testid="profile-auth-bindings-panel"
          :user="user"
          :linuxdo-enabled="linuxdoEnabled"
          :dingtalk-enabled="dingtalkEnabled"
          :oidc-enabled="oidcEnabled"
          :oidc-provider-name="oidcProviderName"
          :wechat-enabled="wechatEnabled"
          :wechat-open-enabled="wechatOpenEnabled"
          :wechat-mp-enabled="wechatMpEnabled"
          compact
        />

        <section v-if="sourceHints.length" class="card">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <h2 class="text-lg font-medium text-gray-900 dark:text-white">
              {{ t('profile.linkedProfileSources') }}
            </h2>
          </div>
          <div class="grid gap-3 px-6 py-6">
            <div
              v-for="hint in sourceHints"
              :key="hint.key"
              class="flex items-start gap-3 rounded-surface border border-gray-100 bg-gray-50/80 px-4 py-3 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-900/30 dark:text-gray-300"
            >
              <Icon name="link" size="sm" class="mt-0.5 text-gray-400 dark:text-gray-500" />
              <span>{{ hint.text }}</span>
            </div>
          </div>
        </section>
      </div>

      <div data-testid="profile-side-column" class="min-w-0 space-y-4">
        <slot name="side" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import UserIdBadge from '@/components/user/UserIdBadge.vue'
import ProfileAvatarCard from '@/components/user/profile/ProfileAvatarCard.vue'
import ProfileEditForm from '@/components/user/profile/ProfileEditForm.vue'
import ProfileIdentityBindingsSection from '@/components/user/profile/ProfileIdentityBindingsSection.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type {
  User,
  UserAuthBindingStatus,
  UserAuthProvider,
  UserProfileSourceContext,
} from '@/api/identity.types'

// ProfileInfoCard.vue presents the account profile, balance, membership date, and external
// identity source hints. It delegates avatar, username, and binding edits to their profile
// sections, while compatibility readers interpret both direct and nested binding data
// supplied by the API.
//
// A direct email binding flag takes precedence over nested evidence before a synthetic
// .invalid email is shown. Avatar and username provenance use ordered aliases because source
// information can appear in several record shapes. Recognized external providers receive
// either their supplied display label or a localized provider name, and unsupported source
// values produce no hint.

// Profile and provider-availability props determine the displayed account details and
// binding actions; explicit WeChat capabilities retain their distinction from legacy
// combined availability.
const props = withDefaults(
  defineProps<{
    user: User | null // Current account profile.
    linuxdoEnabled?: boolean // Linux DO binding availability.
    dingtalkEnabled?: boolean // DingTalk binding availability.
    oidcEnabled?: boolean // OIDC binding availability.
    oidcProviderName?: string // Configured OIDC display name.
    wechatEnabled?: boolean // Legacy combined WeChat availability.
    wechatOpenEnabled?: boolean // Optional Open Platform availability.
    wechatMpEnabled?: boolean // Optional public-account WeChat availability.
  }>(),
  {
    linuxdoEnabled: false, // Linux DO binding availability.
    dingtalkEnabled: false, // DingTalk binding availability.
    oidcEnabled: false, // OIDC binding availability.
    oidcProviderName: 'OIDC', // Configured OIDC display name.
    wechatEnabled: false, // Legacy combined WeChat availability.
    wechatOpenEnabled: undefined, // Optional Open Platform availability.
    wechatMpEnabled: undefined, // Optional public-account WeChat availability.
  },
)

// Translations and configured balance formatting supply the account summary and provider-
// source hints for the profile header.
const { t } = useI18n()
// Profile amounts use the same administrator-configured balance unit as other account pages.
const { formatBalanceAmount } = useBalanceDisplay()

// normalizeBindingStatus converts boolean and detailed identity records into a nullable
// bound flag. An explicit bound boolean wins even when subject, issuer, or provider-key
// metadata is present, preventing an unbound detailed record from becoming bound through
// leftover identity fields. A missing record returns null so the caller can apply its own
// absence rule. Detailed records without an explicit flag use the presence of those identity
// fields as their compatible bound indication.
function normalizeBindingStatus(
  binding: boolean | UserAuthBindingStatus | undefined,
): boolean | null {
  if (typeof binding === 'boolean') {
    return binding
  }
  if (!binding) {
    return null
  }
  if (typeof binding.bound === 'boolean') {
    return binding.bound
  }
  return Boolean(binding.provider_subject || binding.issuer || binding.provider_key)
}

// isEmailBound resolves whether the account has a bound email for primaryEmailDisplay.
// A direct email_bound boolean wins, including false, so nested identity evidence cannot
// override an explicit unbound state. When that flag is absent, auth_bindings.email takes
// precedence over identity_bindings.email through nullish selection, and the selected record
// is normalized once. Missing or unresolved evidence returns false, which makes the display
// suppress synthetic .invalid addresses until binding is confirmed.
function isEmailBound(user: User | null | undefined): boolean {
  if (typeof user?.email_bound === 'boolean') {
    return user.email_bound
  }

  const nested = user?.auth_bindings?.email ?? user?.identity_bindings?.email
  const normalized = normalizeBindingStatus(nested)
  return normalized ?? false
}

// Avatar and display-name values use trimmed profile strings; the email and membership
// labels below handle placeholders and invalid dates separately.
const avatarUrl = computed(() => props.user?.avatar_url?.trim() || '')
const displayName = computed(
  () => props.user?.username?.trim() || props.user?.email?.trim() || t('profile.user'),
)
// primaryEmailDisplay suppresses synthetic .invalid addresses until email binding is
// confirmed, keeping placeholder account identities out of the contact display.
const primaryEmailDisplay = computed(() => {
  const email = props.user?.email?.trim() || ''
  if (!email) {
    return ''
  }
  if (email.endsWith('.invalid') && !isEmailBound(props.user)) {
    return ''
  }
  return email
})
// memberSinceLabel formats a valid account creation date as month and year, using a dash
// when the timestamp is absent or invalid.
const memberSinceLabel = computed(() => {
  const raw = props.user?.created_at?.trim()
  if (!raw) {
    return '-'
  }

  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) {
    return '-'
  }

  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
  }).format(date)
})

// providerLabels supplies localized source names, including the configured OIDC label, for
// profile fields populated by external identities.
const providerLabels = computed<Record<UserAuthProvider, string>>(() => ({
  email: t('profile.authBindings.providers.email'),
  linuxdo: t('profile.authBindings.providers.linuxdo'),
  dingtalk: t('profile.authBindings.providers.dingtalk'),
  oidc: t('profile.authBindings.providers.oidc', { providerName: props.oidcProviderName }),
  wechat: t('profile.authBindings.providers.wechat'),
  github: 'GitHub',
  google: 'Google',
}))

// normalizeProvider recognizes the source-provider spellings accepted by this profile
// summary. OIDC prefixes collapse to oidc, while unknown values return null so they do not
// create a source hint.
function normalizeProvider(value: string): UserAuthProvider | null {
  const normalized = value.trim().toLowerCase()
  if (
    normalized === 'email' ||
    normalized === 'linuxdo' ||
    normalized === 'wechat' ||
    normalized === 'github' ||
    normalized === 'google'
  ) {
    return normalized
  }
  if (normalized === 'oidc' || normalized.startsWith('oidc:') || normalized.startsWith('oidc/')) {
    return 'oidc'
  }
  return null
}

// readObjectString selects the first nonempty trimmed string from the supplied property
// names. Their order defines precedence across compatible server field spellings, and no
// usable property returns an empty string.
function readObjectString(source: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = source[key]
    if (typeof value === 'string' && value.trim()) {
      return value.trim()
    }
  }
  return ''
}

// resolveThirdPartySource resolves a provider and label from a string or compatible source
// record. Ordered property aliases support several server representations, and an explicit
// nonempty display label overrides the localized provider label. Email and unrecognized
// providers return null because these hints describe external profile sources. The caller
// separately chooses which avatar or username source field takes precedence before passing
// it into this converter.
function resolveThirdPartySource(rawSource: string | UserProfileSourceContext | null | undefined): {
  provider: UserAuthProvider // Recognized external identity provider.
  label: string // Chosen provider display label.
} | null {
  if (!rawSource) {
    return null
  }

  if (typeof rawSource === 'string') {
    const provider = normalizeProvider(rawSource)
    if (!provider || provider === 'email') {
      return null
    }
    return {
      provider,
      label: providerLabels.value[provider],
    }
  }

  const sourceRecord = rawSource as Record<string, unknown>
  const provider = normalizeProvider(
    readObjectString(sourceRecord, 'provider', 'source', 'provider_type', 'auth_provider'),
  )
  if (!provider || provider === 'email') {
    return null
  }

  const explicitLabel = readObjectString(
    sourceRecord,
    'provider_label',
    'label',
    'provider_name',
    'providerName',
  )

  return {
    provider,
    label: explicitLabel || providerLabels.value[provider],
  }
}

// sourceHints resolves avatar and username provenance through ordered profile-source
// aliases, emitting only recognized external-provider explanations for the displayed account
// fields.
const sourceHints = computed(() => {
  const currentUser = props.user
  if (!currentUser) {
    return []
  }

  const hints: Array<{
    key: string // Profile source field identity.
    text: string // Localized provenance hint text.
  }> = []
  const avatarSource = resolveThirdPartySource(
    currentUser.profile_sources?.avatar ?? currentUser.avatar_source,
  )
  const usernameSource = resolveThirdPartySource(
    currentUser.profile_sources?.username ??
      currentUser.profile_sources?.display_name ??
      currentUser.profile_sources?.nickname ??
      currentUser.display_name_source ??
      currentUser.username_source ??
      currentUser.nickname_source,
  )

  if (avatarSource) {
    hints.push({
      key: 'avatar',
      text: t('profile.authBindings.source.avatar', { providerName: avatarSource.label }),
    })
  }

  if (usernameSource) {
    hints.push({
      key: 'username',
      text: t('profile.authBindings.source.username', { providerName: usernameSource.label }),
    })
  }

  return hints
})
</script>
