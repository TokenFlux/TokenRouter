<template>
  <div v-if="shouldShowQuota">
    <!-- First line: Platform + Tier Badge -->
    <div class="mb-1 flex items-center gap-1">
      <span :class="['badge text-xs px-2 py-0.5 rounded-compact font-medium', tierBadgeClass]">
        {{ tierLabel }}
      </span>
    </div>

    <!-- Unrestricted display label or active provider cooldown. -->
    <div class="text-xs text-gray-400 dark:text-gray-500">
      <span v-if="!isRateLimited">
        {{ t('admin.providers.gemini.rateLimit.unlimited') }}
      </span>
      <span
        v-else
        :class="[
          'font-medium',
          isUrgent
            ? 'text-red-600 dark:text-red-400 animate-pulse'
            : 'text-amber-600 dark:text-amber-400',
        ]"
      >
        {{ t('admin.providers.gemini.rateLimit.limited', { time: resetCountdown }) }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Provider, GeminiCredentials } from '@/api/admin/providers.types'

/**
 * ProviderQuotaInfo.vue displays Gemini channel tiers and the provider-level rate-limit
 * countdown. Credential markers select Code Assist or AI Studio labels, including legacy tier
 * spellings. Third-party API-key credentials suppress this official-quota display, and invalid
 * reset timestamps do not activate rate-limit presentation.
 *
 * The local clock advances once per second while a valid future reset is active. Its watcher
 * starts the timer for an initially limited provider and stops it after expiry; unmount
 * releases any remaining timer. Countdown text uses whole seconds, minutes, or hours, with a
 * separate urgent state during the last minute. Tier labels describe the saved credential
 * markers rather than a fresh upstream entitlement query.
 */

/**
 * props supplies Gemini credentials and provider-level reset metadata used by channel labels,
 * rate-limit visibility, and the countdown clock.
 */
const props = defineProps<{
  provider: Provider // current Gemini provider record
}>()

/**
 * Translation and a component-owned second clock provide live reset wording while a future
 * provider rate limit is active.
 */
const { t } = useI18n()

const now = ref(new Date())
let timer: ReturnType<typeof setInterval> | null = null

/**
 * Gemini channel predicates recognize explicit OAuth types and the legacy project_id marker
 * when oauth_type is absent on the record.
 */
const isCodeAssist = computed(() => {
  const creds = props.provider.credentials as GeminiCredentials | undefined
  // Older credentials identify Code Assist by project_id only when oauth_type is absent,
  // keeping explicit channel selections authoritative.
  return creds?.oauth_type === 'code_assist' || (!creds?.oauth_type && !!creds?.project_id)
})

/**
 * shouldShowQuota exposes official Gemini tier information while excluding API-key records
 * marked as third-party providers in their credential metadata.
 */
const shouldShowQuota = computed(() => {
  const credentials = props.provider.credentials as GeminiCredentials | undefined
  return (
    props.provider.platform === 'gemini' &&
    !(props.provider.type === 'apikey' && credentials?.provider_type === 'third_party')
  )
})

/**
 * tierLabel recognizes current and legacy channel tier markers, with generic channel names used
 * when the saved tier is absent.
 */
const tierLabel = computed(() => {
  const creds = props.provider.credentials as GeminiCredentials | undefined

  if (isCodeAssist.value) {
    const tier = (creds?.tier_id || '').toString().trim().toLowerCase()
    if (tier === 'gcp_enterprise') return 'GCP Enterprise'
    if (tier === 'gcp_standard' || tier === 'legacy-tier' || tier === 'free-tier') {
      return 'GCP Standard'
    }
    // Interpret legacy tier markers within the selected channel so older credential records
    // continue to display their stored subscription level.
    const upper = (creds?.tier_id || '').toString().trim().toUpperCase()
    if (upper.includes('ULTRA') || upper.includes('ENTERPRISE')) return 'GCP Enterprise'
    if (upper) return `GCP ${upper}`
    return 'GCP'
  }

  // Credentials outside Code Assist use AI Studio tier labels, with a generic label when
  // a recognized tier is absent.
  const tier = (creds?.tier_id || '').toString().trim().toLowerCase()
  if (tier === 'aistudio_paid') return 'AI Studio Pay-as-you-go'
  if (tier === 'aistudio_free') return 'AI Studio Free Tier'
  return 'AI Studio'
})

/**
 * tierBadgeClass uses the same channel and tier markers as the label to distinguish enterprise,
 * paid, and free presentation colors.
 */
const tierBadgeClass = computed(() => {
  const creds = props.provider.credentials as GeminiCredentials | undefined

  if (isCodeAssist.value) {
    const tier = (creds?.tier_id || '').toString().trim().toLowerCase()
    if (tier === 'gcp_enterprise')
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    if (tier === 'gcp_standard' || tier === 'legacy-tier' || tier === 'free-tier')
      return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    // Interpret legacy tier markers within the selected channel so older credential records
    // continue to display their stored subscription level.
    const upper = (creds?.tier_id || '').toString().trim().toUpperCase()
    if (upper.includes('ULTRA') || upper.includes('ENTERPRISE'))
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  // AI Studio uses the paid palette when its tier marker is unknown, while an explicit free
  // marker remains visually neutral.
  const tier = (creds?.tier_id || '').toString().trim().toLowerCase()
  if (tier === 'aistudio_paid')
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  if (tier === 'aistudio_free')
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200'
  return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
})

/**
 * Rate-limit display requires a parseable future reset timestamp; countdown and urgency use the
 * same current second clock and saved deadline.
 */
const isRateLimited = computed(() => {
  if (!props.provider.rate_limit_reset_at) return false
  const resetTime = Date.parse(props.provider.rate_limit_reset_at)
  // An invalid deadline cannot establish an active cooldown, so the component leaves the
  // rate-limit indicator and timer inactive.
  if (Number.isNaN(resetTime)) return false
  return resetTime > now.value.getTime()
})

/**
 * resetCountdown displays whole seconds, minutes, or hours until the reset and provides missing
 * or invalid-date feedback before formatting.
 */
const resetCountdown = computed(() => {
  if (!props.provider.rate_limit_reset_at) return ''
  const resetTime = Date.parse(props.provider.rate_limit_reset_at)
  // Keep invalid reset text as an unknown countdown rather than deriving time units from an
  // unusable timestamp value.
  if (Number.isNaN(resetTime)) return '-'

  const diffMs = resetTime - now.value.getTime()
  if (diffMs <= 0) return t('admin.providers.gemini.rateLimit.now')

  // Truncate subsecond duration before composing visible countdown units.
  const diffSeconds = Math.floor(diffMs / 1000)
  const diffMinutes = Math.floor(diffSeconds / 60)
  const diffHours = Math.floor(diffMinutes / 60)

  if (diffMinutes < 1) return `${diffSeconds}s`
  if (diffHours < 1) {
    const secs = diffSeconds % 60
    return `${diffMinutes}m ${secs}s`
  }
  const mins = diffMinutes % 60
  return `${diffHours}h ${mins}m`
})

/**
 * isUrgent marks an active rate limit during its final minute and excludes missing, invalid, or
 * already elapsed reset timestamps.
 */
const isUrgent = computed(() => {
  if (!props.provider.rate_limit_reset_at) return false
  const resetTime = Date.parse(props.provider.rate_limit_reset_at)
  // Invalid timestamps cannot establish a final-minute deadline, so they leave the urgency
  // treatment disabled for this provider row.
  if (Number.isNaN(resetTime)) return false

  const diffMs = resetTime - now.value.getTime()
  // Highlight the final minute of an active cooldown.
  return diffMs > 0 && diffMs < 60000
})

// Tie the second clock to active rate limiting so ordinary rows do not retain a timer after
// their deadline passes.
watch(
  () => isRateLimited.value,
  (limited) => {
    if (limited && !timer) {
      // Start one clock for this component so a valid active limit counts down and eventually
      // clears its own presentation.
      timer = setInterval(() => {
        now.value = new Date()
      }, 1000)
    } else if (!limited && timer) {
      // Stop the clock once the deadline is inactive so completed countdowns no longer schedule
      // updates for the provider row.
      clearInterval(timer)
      timer = null
    }
  },
  { immediate: true }, // Initialize timer ownership from the first observed limit state.
)

onUnmounted(() => {
  if (timer !== null) {
    // Release the timer on unmount even if a future limit remains active, preventing updates to
    // the removed row.
    clearInterval(timer)
    timer = null
  }
})
</script>
