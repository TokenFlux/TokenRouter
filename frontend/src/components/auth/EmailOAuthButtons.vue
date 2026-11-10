<template>
  <div v-if="hasProviders" class="space-y-4">
    <div v-if="showDivider" class="flex items-center gap-3">
      <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
      <span class="text-xs text-gray-500 dark:text-dark-400">
        {{ t('auth.oauthOrContinue') }}
      </span>
      <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
    </div>

    <div :class="providerGridClass">
      <button
        v-for="provider in visibleProviders"
        :key="provider"
        type="button"
        :disabled="disabled"
        class="btn btn-secondary w-full justify-center gap-2"
        @click="startLogin(provider)"
      >
        <GitHubMark v-if="provider === 'github'" class="h-5 w-5 text-gray-800 dark:text-gray-100" />
        <GoogleMark v-else class="h-5 w-5" />
        <span class="font-medium">{{ providerLabel(provider) }}</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import GitHubMark from './GitHubMark.vue'
import GoogleMark from '@/components/common/GoogleMark.vue'
import type { OAuthLoginStart } from '@/api/auth'
import { resolveAffiliateCode, storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'

// EmailOAuthButtons.vue presents configured GitHub and Google sign-in buttons and
// emits provider start requests. The parent owns consent and captcha gating, while
// these buttons remember provider identity and referral attribution for the return
// trip.
//
// The provider marker goes into session storage before the event is emitted so the
// shared callback can forward code and state to the correct backend provider
// endpoint. Nonempty referral and promotion values also travel in the request
// parameters. A single provider receives a descriptive full-width label, while two
// providers use short names and a responsive two-column layout.

// EmailOAuthProvider identifies the email-backed providers supported by these
// sign-in buttons.
type EmailOAuthProvider = 'github' | 'google'
// The session-storage key connects an emitted email-provider start request to
// backend forwarding when code and state return through the shared frontend
// callback.
const EMAIL_OAUTH_PENDING_PROVIDER_KEY = 'email_oauth_pending_provider' // email-provider callback session key

// props supplies parent-owned configuration and draft input. emit reports
// authentication actions back to that owner, which decides whether to submit or
// navigate.
const props = withDefaults(
  defineProps<{
    disabled?: boolean // parent action disable flag

    affCode?: string // parent referral code

    promoCode?: string // optional promotion code

    githubEnabled?: boolean // GitHub button visibility

    googleEnabled?: boolean // Google button visibility

    // showDivider defaults to true when omitted. Parents can hide the separator
    // when composing several provider sections in the same authentication form.
    showDivider?: boolean // provider separator visibility
  }>(),
  {
    showDivider: true,
  },
) // parent component inputs
const emit = defineEmits<{
  start: [request: OAuthLoginStart] // parent authentication start request
}>() // parent component event dispatcher

// route and router connect callback or sign-in handlers to query values and
// navigation. Authentication stores and translators supply the session actions and
// notifications used by those handlers.
const route = useRoute() // current callback query context
const { t } = useI18n() // translated authentication message resolver

// visibleProviders keeps GitHub before Google when both are enabled. hasProviders,
// hasMultipleProviders, and providerGridClass derive section visibility, compact
// labels, and responsive columns from that list.
const visibleProviders = computed<EmailOAuthProvider[]>(() => {
  const providers: EmailOAuthProvider[] = []
  if (props.githubEnabled) providers.push('github')
  if (props.googleEnabled) providers.push('google')
  return providers
}) // enabled email-provider button order

const hasProviders = computed(() => visibleProviders.value.length > 0) // provider section visibility
const hasMultipleProviders = computed(() => visibleProviders.value.length > 1) // multi-provider presentation flag
const providerGridClass = computed(() => [
  'grid',
  'grid-cols-1',
  'gap-3',
  hasMultipleProviders.value ? 'sm:grid-cols-2' : '',
]) // responsive provider button layout

// providerLabel selects a compact provider name when multiple buttons are visible.
// A single enabled provider uses the translated sign-in phrase so the full-width
// button describes its action.
function providerLabel(provider: EmailOAuthProvider): string {
  const name = provider === 'github' ? 'GitHub' : 'Google'
  return hasMultipleProviders.value ? name : t('auth.emailOAuth.signIn', { providerName: name })
}

// startLogin remembers the selected email provider and emits its authorization
// request. It resolves referral attribution before saving the provider marker,
// then includes nonempty referral and promotion parameters with the current
// redirect. The session marker lets the shared frontend callback associate
// returned code and state with the backend provider endpoint. The parent still
// owns consent and captcha gating before it navigates to that provider.
function startLogin(provider: EmailOAuthProvider): void {
  const redirectTo = (route.query.redirect as string) || '/dashboard'
  const affiliateCode = resolveAffiliateCode(props.affCode, route.query.aff, route.query.aff_code)
  // Save referral attribution before leaving the current sign-in entry, allowing
  // the callback to include it if the provider requires account creation.
  storeOAuthAffiliateCode(affiliateCode)
  // Remember the provider before emitting the start request so its returning
  // frontend callback can forward code and state to the matching backend endpoint.
  window.sessionStorage.setItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY, provider)
  const params: Record<string, string> = { redirect: redirectTo }
  if (affiliateCode) {
    params.aff_code = affiliateCode
  }
  const promoCode = props.promoCode?.trim()
  if (promoCode) {
    params.promo_code = promoCode
  }
  emit('start', { provider, params })
}
</script>
