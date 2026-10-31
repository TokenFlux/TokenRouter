<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import CaptchaChallenge from '@/components/auth/CaptchaChallenge.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { useAppStore } from '@/stores/app'

// TrCaptcha.vue adapts the site's selected challenge to the plugin token contract.
// The public settings request must succeed before disabled verification can emit
// an empty token. Provider failures retain an actionable retry state, and Tencent
// combines its ticket and random proof into the JSON format accepted by the host.
//
// Provider settings and component lifetime can change while an action challenge is
// open. Each action captures the current configuration generation; later completion
// is ignored after replacement or unmount. Inline challenge callbacks use the mounted
// provider identity, while the shared native component owns provider SDK teardown.

// The store and translator supply current site configuration and existing challenge
// labels; widget state belongs only to this mounted plugin component instance.
const app = useAppStore()
const { t } = useI18n()
const widget = ref<InstanceType<typeof CaptchaChallenge> | null>(null)
const loading = ref(true)
const failed = ref(false)
const verifying = ref(false)
const verified = ref(false)
let active = true
let generation = 0

// Verified proof is opaque to plugin pages; expiration invalidates any previously
// supplied proof, including when an administrator changes the selected provider.
const emit = defineEmits<{
  verified: [token: string] // Current provider proof token.
  expired: [] // Previously issued proof invalidation.
}>()

// Provider selection follows native priority and sends only one enabled flag to
// CaptchaChallenge, whose inline and action methods otherwise have different precedence.
const settings = computed(() => app.cachedPublicSettings)
const provider = computed(() => {
  if (settings.value?.turnstile_enabled) return 'turnstile'
  if (settings.value?.tencent_captcha_enabled) return 'tencent'
  if (settings.value?.aliyun_captcha_enabled) return 'aliyun'
  return 'none'
})
const configured = computed(() => {
  if (!settings.value) return false
  if (provider.value === 'turnstile') return !!settings.value.turnstile_site_key
  if (provider.value === 'tencent') return !!settings.value.tencent_captcha_app_id
  if (provider.value === 'aliyun')
    return !!settings.value.aliyun_captcha_scene_id && !!settings.value.aliyun_captcha_prefix
  return true
})
const providerIdentity = computed(() =>
  JSON.stringify([
    provider.value,
    settings.value?.turnstile_site_key,
    settings.value?.tencent_captcha_app_id,
    settings.value?.tencent_captcha_region,
    settings.value?.aliyun_captcha_scene_id,
    settings.value?.aliyun_captcha_prefix,
    settings.value?.aliyun_captcha_region,
  ]),
)

/** readSettings obtains a confirmed site configuration before allowing disabled verification.
 * Fetch failure keeps the challenge unavailable and permits retry. Unmounted instances
 * discard completion while the store may still retain its shared public result.
 */
async function readSettings(): Promise<void> {
  const retry = failed.value
  loading.value = true
  failed.value = false
  const result = await app.fetchPublicSettings(retry)
  if (!active) return
  loading.value = false
  failed.value = !result || !configured.value
  if (result && provider.value === 'none') emit('verified', '')
}

/** acceptProof publishes the selected provider's opaque token to its plugin page.
 * Tencent preserves both proof fields in JSON. Callbacks after unmount are ignored,
 * and successful verification clears an earlier error without changing site settings.
 */
function acceptProof(token: string, randstr: string): void {
  if (!active || loading.value) return
  verified.value = true
  failed.value = false
  emit(
    'verified',
    provider.value === 'tencent' ? JSON.stringify({ ticket: token, randstr }) : token,
  )
}

/** expireProof clears local success and tells the page to discard its current proof. */
function expireProof(): void {
  verified.value = false
  emit('expired')
}

/** challengeFailed preserves a visible retry action after a provider rejects verification. */
function challengeFailed(): void {
  expireProof()
  failed.value = true
}

/** verifyAction runs one native action challenge for the current provider generation.
 * Repeated clicks share the disabled button state. Changing provider or leaving the
 * page invalidates pending proof, so its eventual result cannot authorize a later page.
 *
 * CaptchaChallenge can also emit Aliyun proof during verification. The local success
 * flag prevents returning the same proof twice after the action promise resolves.
 * A canceled challenge leaves the page unverified and available for another attempt.
 */
async function verifyAction(): Promise<void> {
  if (verifying.value) return
  const selected = generation
  verifying.value = true
  verified.value = false
  failed.value = false
  widget.value?.reset()
  const result = await widget.value?.verifyAction()
  if (!active || selected !== generation) return
  verifying.value = false
  if (result && !verified.value) acceptProof(result.token, result.randstr)
}

watch(providerIdentity, () => {
  // Invalidate open action dialogs before remounting their provider, preventing a
  // late challenge result from being attributed to the newly selected configuration.
  generation++
  verifying.value = false
  expireProof()
  if (!loading.value && settings.value && provider.value === 'none') emit('verified', '')
})
onMounted(readSettings)
onBeforeUnmount(() => {
  active = false
  generation++
})
</script>

<template>
  <ContentSkeleton v-if="loading" :rows="1" />
  <div v-else-if="failed || !settings || provider !== 'none'" class="space-y-2">
    <CaptchaChallenge
      v-if="settings && configured"
      :key="providerIdentity"
      ref="widget"
      :turnstile-enabled="provider === 'turnstile'"
      :turnstile-site-key="settings.turnstile_site_key || ''"
      :tencent-enabled="provider === 'tencent'"
      :tencent-app-id="settings.tencent_captcha_app_id || ''"
      :tencent-region="settings.tencent_captcha_region"
      :aliyun-enabled="provider === 'aliyun'"
      :aliyun-scene-id="settings.aliyun_captcha_scene_id || ''"
      :aliyun-prefix="settings.aliyun_captcha_prefix || ''"
      :aliyun-region="settings.aliyun_captcha_region"
      @verify="acceptProof"
      @expire="expireProof"
      @error="challengeFailed"
    />
    <p v-if="failed" role="alert" class="text-sm text-red-600 dark:text-red-400">{{
      t('auth.verifyFailed')
    }}</p>
    <button
      v-if="!configured || (failed && provider === 'none')"
      type="button"
      class="btn btn-secondary"
      @click="readSettings"
      >{{ t('common.retry') }}</button
    >
    <button
      v-else-if="provider === 'tencent' || provider === 'aliyun'"
      type="button"
      class="btn btn-secondary"
      :disabled="verifying"
      @click="verifyAction"
    >
      {{
        verifying
          ? t('auth.captchaVerifying')
          : verified
            ? t('auth.captchaVerified')
            : t('auth.captchaClickToVerify')
      }}
    </button>
    <button v-else-if="failed" type="button" class="btn btn-secondary" @click="widget?.reset()">{{
      t('common.retry')
    }}</button>
  </div>
</template>
