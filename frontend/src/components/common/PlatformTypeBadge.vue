<template>
  <div class="inline-flex flex-col gap-0.5 text-xs font-medium">
    <div class="inline-flex items-center overflow-hidden rounded-compact">
      <span :class="['inline-flex items-center gap-1 px-2 py-1', platformClass]">
        <PlatformIcon :platform="platform" size="xs" />
        <span class="self-baseline">{{ platformLabel }}</span>
      </span>
      <span :class="['inline-flex items-center gap-1 px-1.5 py-1', typeClass]">
        <Icon name="key" size="xs" v-if="type === 'oauth'" class="h-3 w-3" />

        <Icon v-else-if="type === 'setup-token'" name="shield" size="xs" />

        <Icon v-else-if="type === 'service_account'" name="cloud" size="xs" />
        <Icon v-else name="key" size="xs" />
        <span class="self-baseline">{{ typeLabel }}</span>
      </span>
    </div>

    <div
      v-if="planLabel || privacyBadge"
      class="inline-flex items-center overflow-hidden rounded-compact"
    >
      <span
        v-if="planLabel"
        :class="['inline-flex items-center gap-1 px-1.5 py-1', planBadgeClass]"
      >
        <GrokFreeIcon v-if="isGrokFreePlan" data-testid="grok-free-plan-icon" />
        <Icon
          v-else-if="planIconName"
          :name="planIconName"
          size="xs"
          data-testid="grok-plan-icon"
          aria-hidden="true"
        />
        <span class="self-baseline">{{ planLabel }}</span>
      </span>
      <span
        v-if="privacyBadge"
        :class="['inline-flex items-center gap-1 px-1.5 py-1', privacyBadge.class]"
        :title="privacyBadge.title"
      >
        <Icon :name="privacyBadge.icon" size="xs" :animate-on-hover="false" />
        <span class="self-baseline">{{ privacyBadge.label }}</span>
      </span>
    </div>

    <div
      v-if="expiresLabel"
      class="text-xs leading-tight text-gray-400 dark:text-gray-500 pl-0.5"
      :title="subscriptionExpiresAt"
    >
      {{ expiresLabel }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ProviderPlatform, ProviderType } from '@/api/admin/providers.types'
import { platformBadgeLightClass, platformLabel as getPlatformLabel } from '@/utils/platformColors'
import PlatformIcon from './PlatformIcon.vue'
import GrokFreeIcon from './GrokFreeIcon.vue'
import Icon from '@/components/icons/Icon.vue'

/**
 * PlatformTypeBadge.vue combines provider platform, authentication type, subscription
 * tier, privacy result, and expiration into compact rows. Plan and authentication
 * aliases are normalized before display, paid tiers can show a local calendar date,
 * and privacy details appear for supported OAuth platforms.
 */

/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()

/**
 * Props describes the caller supplied values consumed by PlatformTypeBadge.
 */
interface Props {
  platform: ProviderPlatform /* provider platform identifier */
  type: ProviderType /* provider credential type */
  /**
   * authMode specializes the credential label when platform is OpenAI and type is OAuth;
   * other provider combinations use their credential type label.
   */
  authMode?: string /* authentication mode subtype */
  planType?: string /* subscription plan identifier */
  /**
   * privacyMode supplies a privacy badge for recognized results when type is OAuth and
   * platform is OpenAI or Antigravity; other combinations omit that badge.
   */
  privacyMode?: string /* privacy operation result */
  /**
   * subscriptionExpiresAt supplies the date row when it parses successfully and planType
   * is present with a normalized value outside the free, basic, and xbasic aliases.
   */
  subscriptionExpiresAt?: string /* subscription expiration timestamp */
}

/**
 * props supplies provider identity and optional account metadata; computed labels
 * decide which plan, privacy, and expiration rows exist.
 */
const props = defineProps<Props>()

/**
 * platformLabel reads known display names from the shared platform catalog.
 * Unknown identifiers keep their supplied text so the badge retains the
 * provider identity when the catalog lacks an entry.
 */
const platformLabel = computed(() => getPlatformLabel(props.platform))

/**
 * normalizedAuthMode removes case and separator differences before distinguishing
 * OpenAI agent identity and personal access token authentication.
 */
const normalizedAuthMode = computed(() =>
  (props.authMode || '')
    .trim()
    .toLowerCase()
    /**
     * Remove separator variations from provider metadata before comparing
     * authentication or subscription aliases in the badge display selectors.
     */
    .replace(/[\s_-]+/g, ''),
)

/**
 * typeLabel specializes OpenAI OAuth modes and otherwise maps provider credential
 * types to compact display labels.
 */
const typeLabel = computed(() => {
  if (props.platform === 'openai' && props.type === 'oauth') {
    if (normalizedAuthMode.value === 'agentidentity') return 'Agent Identity'
    if (normalizedAuthMode.value === 'personalaccesstoken') return 'PAT'
  }
  switch (props.type) {
    case 'oauth':
      return 'OAuth'
    case 'setup-token':
      return 'Token'
    case 'apikey':
      return 'Key'
    case 'bedrock':
      return 'AWS'
    case 'service_account':
      return 'Vertex'
    case 'cosy':
      return 'COSY'
    default:
      return props.type
  }
})

/**
 * normalizedPlanType removes case and separators before plan aliases determine labels,
 * graphics, colors, and expiration eligibility.
 */
const normalizedPlanType = computed(() =>
  (props.planType || '')
    .trim()
    .toLowerCase()
    /**
     * Remove separator variations from provider metadata before comparing
     * authentication or subscription aliases in the badge display selectors.
     */
    .replace(/[\s_-]+/g, ''),
)

/**
 * planLabel names recognized normalized plan aliases, preserves unknown supplied
 * labels, and leaves an absent plan unlabeled.
 */
const planLabel = computed(() => {
  if (!normalizedPlanType.value) return ''
  switch (normalizedPlanType.value) {
    case 'plus':
      return 'Plus'
    case 'team':
      return 'Team'
    case 'chatgptpro':
    case 'pro':
      return 'Pro'
    case 'ultra':
      return 'Ultra'
    case 'free':
    case 'basic':
      return props.platform === 'grok' ? 'Grok Free' : 'Free'
    case 'supergrok':
      return 'SuperGrok'
    case 'supergroklite':
      return 'SuperGrok Lite'
    case 'supergrokplus':
      return 'SuperGrok Plus'
    case 'supergrokheavy':
      return 'SuperGrok Heavy'
    case 'heavy':
      return 'Heavy'
    case 'xbasic':
      return 'X Basic'
    case 'abnormal':
      return t('admin.providers.subscriptionAbnormal')
    default:
      return props.planType
  }
})

/**
 * isGrokFreePlan identifies the free, basic, and X Basic aliases that receive the
 * dedicated Grok free graphic.
 */
const isGrokFreePlan = computed(
  () =>
    props.platform === 'grok' &&
    (normalizedPlanType.value === 'free' ||
      normalizedPlanType.value === 'basic' ||
      normalizedPlanType.value === 'xbasic'),
)

/**
 * planIconName assigns a bolt to recognized SuperGrok or Heavy patterns on Grok
 * providers, leaving other plans without that icon.
 */
const planIconName = computed<'bolt' | null>(() => {
  if (props.platform !== 'grok') return null

  if (
    normalizedPlanType.value === 'supergrok' ||
    normalizedPlanType.value === 'supergrokheavy' ||
    normalizedPlanType.value === 'heavy' ||
    normalizedPlanType.value.includes('heavy')
  ) {
    return 'bolt'
  }
  return null
})
/**
 * platformClass assigns the platform segment palette and uses the shared neutral
 * badge colors for OpenCode. Platforms without a dedicated branch use blue.
 */
const platformClass = computed(() => {
  if (props.platform === 'opencode') return platformBadgeLightClass(props.platform)
  if (props.platform === 'anthropic') {
    return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
  }
  if (props.platform === 'openai') {
    return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
  }
  if (props.platform === 'antigravity') {
    return 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
  }
  if (props.platform === 'qoder') {
    return 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400'
  }
  if (props.platform === 'grok') {
    return 'bg-zinc-100 text-zinc-700 dark:bg-dark-700 dark:text-dark-200'
  }
  if (props.platform === 'kimi') {
    return 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-400'
  }
  if (props.platform === 'zhipu') {
    return 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-400'
  }
  if (props.platform === 'deepseek') {
    return 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400'
  }
  return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
})

/**
 * typeClass follows the platform palette with the credential segment text tone so both
 * parts read as one badge.
 */
const typeClass = computed(() => {
  if (props.platform === 'opencode') return platformBadgeLightClass(props.platform)
  if (props.platform === 'anthropic') {
    return 'bg-orange-100 text-orange-600 dark:bg-orange-900/30 dark:text-orange-400'
  }
  if (props.platform === 'openai') {
    return 'bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400'
  }
  if (props.platform === 'antigravity') {
    return 'bg-purple-100 text-purple-600 dark:bg-purple-900/30 dark:text-purple-400'
  }
  if (props.platform === 'qoder') {
    return 'bg-cyan-100 text-cyan-600 dark:bg-cyan-900/30 dark:text-cyan-400'
  }
  if (props.platform === 'grok') {
    return 'bg-zinc-100 text-zinc-600 dark:bg-dark-700 dark:text-dark-200'
  }
  if (props.platform === 'kimi') {
    return 'bg-pink-100 text-pink-600 dark:bg-pink-900/30 dark:text-pink-400'
  }
  if (props.platform === 'zhipu') {
    return 'bg-indigo-100 text-indigo-600 dark:bg-indigo-900/30 dark:text-indigo-400'
  }
  if (props.platform === 'deepseek') {
    return 'bg-teal-100 text-teal-600 dark:bg-teal-900/30 dark:text-teal-400'
  }
  return 'bg-blue-100 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400'
})

/**
 * planBadgeClass prioritizes abnormal and free tiers, then Grok paid patterns and
 * common paid plan names before using the credential palette.
 */
const planBadgeClass = computed(() => {
  if (normalizedPlanType.value === 'abnormal') {
    return 'bg-red-100 text-red-600 dark:bg-red-900/30 dark:text-red-400'
  }

  if (
    normalizedPlanType.value === 'free' ||
    normalizedPlanType.value === 'basic' ||
    normalizedPlanType.value === 'xbasic'
  ) {
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200'
  }
  if (props.platform === 'grok' && normalizedPlanType.value) {
    if (normalizedPlanType.value.includes('heavy')) {
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/30 dark:text-purple-300'
    }

    if (normalizedPlanType.value.includes('supergrok')) {
      return 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-300'
    }

    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  }

  if (normalizedPlanType.value === 'plus') {
    return 'bg-sky-100 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300'
  }
  if (normalizedPlanType.value === 'team') {
    return 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300'
  }
  if (normalizedPlanType.value === 'pro' || normalizedPlanType.value === 'chatgptpro') {
    return 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-300'
  }
  if (normalizedPlanType.value === 'ultra') {
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  }
  return typeClass.value
})

/**
 * expiresLabel formats a valid paid subscription expiration in the browser local
 * calendar. Missing dates, missing plans, free aliases, and invalid dates produce no
 * expiration row in the template.
 */
const expiresLabel = computed(() => {
  if (!props.subscriptionExpiresAt || !props.planType) return ''
  if (
    normalizedPlanType.value === 'free' ||
    normalizedPlanType.value === 'basic' ||
    normalizedPlanType.value === 'xbasic'
  )
    return ''
  try {
    const d = new Date(props.subscriptionExpiresAt)
    /**
     * Invalid upstream expiration text produces no date row, preventing a malformed
     * timestamp from appearing as a meaningful subscription deadline.
     */
    if (isNaN(d.getTime())) return ''
    const yyyy = d.getFullYear()
    const mm = String(d.getMonth() + 1).padStart(2, '0')
    const dd = String(d.getDate()).padStart(2, '0')
    return `${t('admin.providers.subscriptionExpires')} ${yyyy}-${mm}-${dd}`
  } catch {
    /**
     * If expiration conversion throws, omit the optional date row and leave the provider
     * identity and plan details available.
     */
    return ''
  }
})

/**
 * privacyBadge exposes supported OpenAI or Antigravity OAuth privacy results as a
 * label, icon, tooltip, and palette; unknown states omit the badge.
 */
const privacyBadge = computed(() => {
  if (props.type !== 'oauth' || !props.privacyMode) return null

  if (props.platform !== 'openai' && props.platform !== 'antigravity') return null

  const shieldCheck = 'shieldCheck' as const
  const shieldX = 'shieldX' as const
  switch (props.privacyMode) {
    case 'training_off':
      return {
        label: 'Private',
        icon: shieldCheck,
        title: t('admin.providers.privacyTrainingOff'),
        class: 'bg-green-100 text-green-600 dark:bg-green-900/30 dark:text-green-400',
      }
    case 'training_set_cf_blocked':
      return {
        label: 'CF',
        icon: shieldX,
        title: t('admin.providers.privacyCfBlocked'),
        class: 'bg-yellow-100 text-yellow-600 dark:bg-yellow-900/30 dark:text-yellow-400',
      }
    case 'training_set_failed':
      return {
        label: 'Fail',
        icon: shieldX,
        title: t('admin.providers.privacyFailed'),
        class: 'bg-red-100 text-red-600 dark:bg-red-900/30 dark:text-red-400',
      }

    case 'privacy_set':
      return {
        label: 'Private',
        icon: shieldCheck,
        title: t('admin.providers.privacyAntigravitySet'),
        class: 'bg-green-100 text-green-600 dark:bg-green-900/30 dark:text-green-400',
      }
    case 'privacy_set_failed':
      return {
        label: 'Fail',
        icon: shieldX,
        title: t('admin.providers.privacyAntigravityFailed'),
        class: 'bg-red-100 text-red-600 dark:bg-red-900/30 dark:text-red-400',
      }
    default:
      return null
  }
})
</script>
