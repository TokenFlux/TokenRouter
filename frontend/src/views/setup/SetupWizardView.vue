<template>
  <div
    class="flex min-h-screen items-center justify-center bg-gradient-to-br from-gray-50 to-gray-100 p-4 dark:from-dark-900 dark:to-dark-800"
  >
    <div v-content-reveal="motionRoute?.path" class="w-full max-w-2xl">
      <div class="mb-8 text-center">
        <div
          class="mb-4 inline-flex h-16 w-16 items-center justify-center rounded-surface bg-gradient-to-br from-primary-500 to-primary-600 shadow-lg"
        >
          <Icon name="cog" size="xl" class="text-white" />
        </div>
        <h1 class="page-title">{{ t('setup.title') }}</h1>
        <p class="mt-2 text-gray-500 dark:text-dark-400">{{ t('setup.description') }}</p>
      </div>

      <!-- Steps retain their fixed installation order while completed entries use check marks. -->
      <div class="mb-8">
        <div class="flex items-center justify-center">
          <template v-for="(step, index) in steps" :key="step.id">
            <div class="flex items-center">
              <div
                :class="[
                  'flex h-10 w-10 items-center justify-center rounded-full text-sm font-semibold transition',
                  currentStep > index
                    ? 'bg-primary-500 text-white'
                    : currentStep === index
                      ? 'bg-primary-500 text-white ring-4 ring-primary-100 dark:ring-primary-900'
                      : 'bg-gray-200 text-gray-500 dark:bg-dark-700 dark:text-dark-400',
                ]"
              >
                <Icon
                  v-if="currentStep > index"
                  name="check"
                  size="md"
                  :stroke-width="2"
                  :animate-on-hover="false"
                />
                <span v-else>{{ index + 1 }}</span>
              </div>
              <span
                class="ml-2 hidden text-sm font-medium sm:inline"
                :class="
                  currentStep >= index
                    ? 'text-gray-900 dark:text-white'
                    : 'text-gray-400 dark:text-dark-500'
                "
              >
                {{ step.title }}
              </span>
            </div>
            <div
              v-if="index < steps.length - 1"
              class="mx-2 h-0.5 w-6 sm:mx-3 sm:w-12"
              :class="currentStep > index ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-700'"
            ></div>
          </template>
        </div>
      </div>

      <div class="rounded-surface bg-white p-8 shadow-xl dark:bg-dark-900">
        <!-- The database step requires a successful connection test before forward navigation is enabled. -->
        <div v-if="currentStep === 0" v-content-reveal class="space-y-6">
          <div class="mb-6 text-center">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('setup.database.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
              {{ t('setup.database.description') }}
            </p>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('setup.database.host') }}</label>
              <input
                v-model="formData.database.host"
                type="text"
                class="input"
                placeholder="localhost"
              />
            </div>
            <div>
              <label class="input-label">{{ t('setup.database.port') }}</label>
              <input
                v-model.number="formData.database.port"
                type="number"
                class="input"
                placeholder="5432"
              />
            </div>
          </div>

          <div
            class="flex items-center justify-between rounded-surface border border-gray-200 p-3 dark:border-dark-700"
          >
            <div>
              <p class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('setup.redis.enableTls') }}
              </p>
              <p class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('setup.redis.enableTlsHint') }}
              </p>
            </div>
            <Toggle v-model="formData.redis.enable_tls" />
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('setup.database.username') }}</label>
              <input
                v-model="formData.database.user"
                type="text"
                class="input"
                placeholder="postgres"
              />
            </div>
            <div>
              <label class="input-label">{{ t('setup.database.password') }}</label>
              <input
                v-model="formData.database.password"
                type="password"
                class="input"
                :placeholder="t('setup.database.passwordPlaceholder')"
              />
            </div>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('setup.database.databaseName') }}</label>
              <input
                v-model="formData.database.dbname"
                type="text"
                class="input"
                placeholder="tokenrouter"
              />
            </div>
            <div>
              <label class="input-label">{{ t('setup.database.sslMode') }}</label>
              <Select
                v-model="formData.database.sslmode"
                :options="[
                  { value: 'disable', label: t('setup.database.ssl.disable') },
                  { value: 'require', label: t('setup.database.ssl.require') },
                  { value: 'verify-ca', label: t('setup.database.ssl.verifyCa') },
                  { value: 'verify-full', label: t('setup.database.ssl.verifyFull') },
                ]"
              />
            </div>
          </div>

          <button
            @click="testDatabaseConnection"
            :disabled="testingDb"
            class="btn btn-secondary w-full"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="testingDb"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            <Icon
              v-else-if="dbConnected"
              name="check"
              size="sm"
              class="mr-2 text-green-500"
              :stroke-width="2"
              :animate-on-hover="false"
            />
            {{
              testingDb
                ? t('setup.status.testing')
                : dbConnected
                  ? t('setup.status.success')
                  : t('setup.status.testConnection')
            }}
          </button>
        </div>

        <!-- Redis uses its own connection result so database success cannot unlock this step. -->
        <div v-if="currentStep === 1" v-content-reveal class="space-y-6">
          <div class="mb-6 text-center">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('setup.redis.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
              {{ t('setup.redis.description') }}
            </p>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('setup.redis.host') }}</label>
              <input
                v-model="formData.redis.host"
                type="text"
                class="input"
                placeholder="localhost"
              />
            </div>
            <div>
              <label class="input-label">{{ t('setup.redis.port') }}</label>
              <input
                v-model.number="formData.redis.port"
                type="number"
                class="input"
                placeholder="6379"
              />
            </div>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('setup.redis.username') }}</label>
              <input
                v-model="formData.redis.username"
                type="text"
                class="input"
                :placeholder="t('setup.redis.usernamePlaceholder')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('setup.redis.password') }}</label>
              <input
                v-model="formData.redis.password"
                type="password"
                class="input"
                :placeholder="t('setup.redis.passwordPlaceholder')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('setup.redis.database') }}</label>
              <input
                v-model.number="formData.redis.db"
                type="number"
                class="input"
                placeholder="0"
              />
            </div>
          </div>

          <div
            class="flex items-center justify-between rounded-surface border border-gray-200 p-3 dark:border-dark-700"
          >
            <div>
              <p class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('setup.redis.enableTls') }}
              </p>
              <p class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('setup.redis.enableTlsHint') }}
              </p>
            </div>
            <Toggle v-model="formData.redis.enable_tls" />
          </div>

          <button
            @click="testRedisConnection"
            :disabled="testingRedis"
            class="btn btn-secondary w-full"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="testingRedis"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            <Icon
              v-else-if="redisConnected"
              name="check"
              size="sm"
              class="mr-2 text-green-500"
              :stroke-width="2"
              :animate-on-hover="false"
            />
            {{
              testingRedis
                ? t('setup.status.testing')
                : redisConnected
                  ? t('setup.status.success')
                  : t('setup.status.testConnection')
            }}
          </button>
        </div>

        <!-- Administrator credentials are compared locally before moving to the final installation summary. -->
        <div v-if="currentStep === 2" v-content-reveal class="space-y-6">
          <div class="mb-6 text-center">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('setup.admin.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
              {{ t('setup.admin.description') }}
            </p>
          </div>

          <div>
            <label class="input-label">{{ t('setup.admin.email') }}</label>
            <input
              v-model="formData.admin.email"
              type="email"
              class="input"
              placeholder="admin@example.com"
            />
          </div>

          <div>
            <label class="input-label">{{ t('setup.admin.password') }}</label>
            <input
              v-model="formData.admin.password"
              type="password"
              class="input"
              :placeholder="t('setup.admin.passwordPlaceholder')"
            />
          </div>

          <div>
            <label class="input-label">{{ t('setup.admin.confirmPassword') }}</label>
            <input
              v-model="confirmPassword"
              type="password"
              class="input"
              :placeholder="t('setup.admin.confirmPasswordPlaceholder')"
            />
            <p
              v-if="confirmPassword && formData.admin.password !== confirmPassword"
              class="input-error-text"
            >
              {{ t('setup.admin.passwordMismatch') }}
            </p>
          </div>
        </div>

        <!-- The summary exposes connection identities while keeping the supplied passwords out of the preview. -->
        <div v-if="currentStep === 3" v-content-reveal class="space-y-6">
          <div class="mb-6 text-center">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('setup.ready.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
              {{ t('setup.ready.description') }}
            </p>
          </div>

          <div class="space-y-4">
            <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-800">
              <h3 class="mb-2 text-sm font-medium text-gray-500 dark:text-dark-400">
                {{ t('setup.ready.database') }}
              </h3>
              <p class="text-gray-900 dark:text-white">
                {{ formData.database.user }}@{{ formData.database.host }}:{{
                  formData.database.port
                }}/{{ formData.database.dbname }}
              </p>
            </div>

            <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-700">
              <h3 class="mb-2 text-sm font-medium text-gray-500 dark:text-dark-400">
                {{ t('setup.ready.redis') }}
              </h3>
              <p class="text-gray-900 dark:text-white">
                {{ formData.redis.host }}:{{ formData.redis.port }}
              </p>
            </div>

            <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-700">
              <h3 class="mb-2 text-sm font-medium text-gray-500 dark:text-dark-400">
                {{ t('setup.ready.adminEmail') }}
              </h3>
              <p class="text-gray-900 dark:text-white">{{ formData.admin.email }}</p>
            </div>
          </div>
        </div>

        <!-- Connection, installation, and readiness failures share this message region across wizard steps. -->
        <div
          v-if="errorMessage"
          class="mt-6 rounded-surface border border-red-200 bg-red-50 p-4 dark:border-red-800/50 dark:bg-red-900/20"
        >
          <div class="flex items-start gap-3">
            <Icon name="exclamationCircle" size="md" class="flex-shrink-0 text-red-500" />
            <p class="text-sm text-red-700 dark:text-red-400">{{ errorMessage }}</p>
          </div>
        </div>

        <!-- Installation acceptance and restarted-service readiness remain separate visible states. -->
        <div
          v-if="installSuccess"
          class="mt-6 rounded-surface border border-green-200 bg-green-50 p-4 dark:border-green-800/50 dark:bg-green-900/20"
        >
          <div class="flex items-start gap-3">
            <Icon
              name="loader"
              size="md"
              :animate-on-hover="false"
              v-if="!serviceReady"
              class="h-5 w-5 flex-shrink-0 animate-spin text-green-500"
            />
            <Icon
              v-else
              name="checkCircle"
              size="md"
              class="flex-shrink-0 text-green-500"
              :animate-on-hover="false"
            />
            <div>
              <p class="text-sm font-medium text-green-700 dark:text-green-400">
                {{ t('setup.status.completed') }}
              </p>
              <p class="mt-1 text-sm text-green-600 dark:text-green-500">
                {{ serviceReady ? t('setup.status.redirecting') : t('setup.status.restarting') }}
              </p>
            </div>
          </div>
        </div>

        <!-- Successful installation removes backward navigation while the readiness poll owns the login redirect. -->
        <div class="mt-8 flex justify-between">
          <button
            v-if="currentStep > 0 && !installSuccess"
            @click="currentStep--"
            class="btn btn-secondary"
          >
            <Icon
              name="chevronLeft"
              size="sm"
              class="mr-2"
              :stroke-width="2"
              :animate-on-hover="false"
            />
            {{ t('common.back') }}
          </button>
          <div v-else></div>

          <button
            v-if="currentStep < 3"
            @click="nextStep"
            :disabled="!canProceed"
            class="btn btn-primary"
          >
            {{ t('common.next') }}
            <Icon
              name="chevronRight"
              size="sm"
              class="ml-2"
              :stroke-width="2"
              :animate-on-hover="false"
            />
          </button>

          <button
            v-else-if="!installSuccess"
            @click="performInstall"
            :disabled="installing"
            class="btn btn-primary"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="installing"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ installing ? t('setup.status.installing') : t('setup.status.completeInstallation') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import { useRoute as useMotionRoute } from 'vue-router'

import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  testDatabase,
  testRedis,
  install,
  probeSetupRestartReady,
  type InstallRequest,
} from '@/api/setup'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'

// SetupWizardView.vue collects database, Redis, administrator, and server settings
// for an unconfigured installation. Connection-test flags gate the first two
// steps, and matching administrator passwords gate the summary step. The final
// action sends the current form to the setup API and marks installation accepted
// before the service becomes ready in normal mode.
//
// Readiness polling runs independently after installation succeeds. It waits for
// the setup-status endpoint to report normal mode, then schedules a full-page
// login redirect. The attempt count bounds the number of probes rather than their
// total duration because individual fetches have no timeout. Connection flags,
// pending reads, and polling timers retain their existing page-lifetime behavior.

// Route state drives the shared reveal animation; translation supplies the
// wizard's step labels and user-facing completion or timeout descriptions.
const motionRoute = useMotionRoute()
const { t } = useI18n()

// steps fixes database, Redis, administrator, and completion order while keeping
// the labels reactive to the current interface language during setup.
const steps = computed(() => [
  { id: 'database', title: t('setup.database.title') },
  { id: 'redis', title: t('setup.redis.title') },
  { id: 'admin', title: t('setup.admin.title') },
  { id: 'complete', title: t('setup.ready.title') },
])

// Navigation and result state distinguish the active step, a shared failure
// message, and installation acceptance before restarted-service readiness is observed.
const currentStep = ref(0)
const errorMessage = ref('')
const installSuccess = ref(false)

// Connection-test flags gate forward navigation independently for each service;
// testing again clears the prior result until the corresponding request succeeds.
const testingDb = ref(false)
const testingRedis = ref(false)
const dbConnected = ref(false)
const redisConnected = ref(false)
// Installation and confirmation state cover the submit action, repeated password
// input, and later readiness signal used by the completion message and redirect.
const installing = ref(false)
const confirmPassword = ref('')
const serviceReady = ref(false)

// getCurrentPort derives the server default from the browser's visible address.
// An explicit port is parsed directly; otherwise HTTPS selects 443 and other
// protocols select 80. This does not discover an internal proxy listener.
const getCurrentPort = (): number => {
  const port = window.location.port
  if (port) {
    return parseInt(port, 10)
  }

  return window.location.protocol === 'https:' ? 443 : 80
}

// formData retains all installation fields across wizard steps using the setup
// API's request shape; passwords stay in this draft until submission or page disposal.
const formData = reactive<InstallRequest>({
  database: {
    host: 'localhost',
    port: 5432,
    user: 'postgres',
    password: '',
    dbname: 'tokenrouter',
    sslmode: 'disable',
  },
  redis: {
    host: 'localhost',
    port: 6379,
    username: '',
    password: '',
    db: 0,
    enable_tls: false,
  },
  admin: {
    email: '',
    password: '',
  },
  server: {
    host: '0.0.0.0',
    port: getCurrentPort(),
    mode: 'release',
  },
})

// Forward navigation uses remembered connection results, then basic account
// checks; editing connection fields does not itself reset the recorded test flags.
const canProceed = computed(() => {
  switch (currentStep.value) {
    case 0:
      return dbConnected.value
    case 1:
      return redisConnected.value
    case 2:
      return (
        formData.admin.email &&
        formData.admin.password.length >= 8 &&
        formData.admin.password === confirmPassword.value
      )
    default:
      return true
  }
})

// testDatabaseConnection probes the current database draft and records its result.
// A new probe clears the previous success and shared error. Failure selects the
// most specific available response message, then releases the database testing flag.
async function testDatabaseConnection() {
  testingDb.value = true
  errorMessage.value = ''
  dbConnected.value = false

  try {
    await testDatabase(formData.database)
    dbConnected.value = true
  } catch (error: unknown) {
    // Error descriptions prefer backend detail, backend message, then client
    // message. The first nonempty value wins before the fixed connection fallback.
    const err = error as {
      // Optional HTTP response envelope.
      response?: {
        // Optional backend error body.
        data?: {
          detail?: string // Preferred backend failure detail.
          message?: string // Fallback backend error message.
        }
      }
      message?: string // Fallback client error message.
    }
    errorMessage.value =
      err.response?.data?.detail ||
      err.response?.data?.message ||
      err.message ||
      'Connection failed'
  } finally {
    testingDb.value = false
  }
}

// testRedisConnection probes the current Redis draft with its independent test flag.
// Success enables forward navigation from the Redis step. Failure retains the
// draft and publishes the preferred backend or client error message.
async function testRedisConnection() {
  testingRedis.value = true
  errorMessage.value = ''
  redisConnected.value = false

  try {
    await testRedis(formData.redis)
    redisConnected.value = true
  } catch (error: unknown) {
    // Error descriptions prefer backend detail, backend message, then client
    // message. The first nonempty value wins before the fixed connection fallback.
    const err = error as {
      // Optional HTTP response envelope.
      response?: {
        // Optional backend error body.
        data?: {
          detail?: string // Preferred backend failure detail.
          message?: string // Fallback backend error message.
        }
      }
      message?: string // Fallback client error message.
    }
    errorMessage.value =
      err.response?.data?.detail ||
      err.response?.data?.message ||
      err.message ||
      'Connection failed'
  } finally {
    testingRedis.value = false
  }
}

// nextStep advances once when the current step passes its existing readiness check.
function nextStep() {
  if (canProceed.value) {
    errorMessage.value = ''
    currentStep.value++
  }
}

// performInstall submits the complete draft and starts readiness polling on success.
// Installation acceptance is published before the detached poll completes. The
// installing flag covers only submission, while errors retain the form for retry.
async function performInstall() {
  installing.value = true
  errorMessage.value = ''

  try {
    await install(formData)
    installSuccess.value = true
    // Start the readiness phase without keeping the installation action loading;
    // its separate status flag controls the eventual full-page login redirect.
    waitForServiceRestart()
  } catch (error: unknown) {
    // Error descriptions prefer backend detail, backend message, then client
    // message. The first nonempty value wins before the fixed installation fallback.
    const err = error as {
      // Optional HTTP response envelope.
      response?: {
        // Optional backend error body.
        data?: {
          detail?: string // Preferred backend failure detail.
          message?: string // Fallback backend error message.
        }
      }
      message?: string // Fallback client error message.
    }
    errorMessage.value =
      err.response?.data?.detail ||
      err.response?.data?.message ||
      err.message ||
      'Installation failed'
  } finally {
    installing.value = false
  }
}

// waitForServiceRestart probes setup status until normal mode is reported.
// After an initial delay, successful HTTP responses must contain a data object
// whose needs_setup value is falsy before readiness is published. That transition schedules
// the login redirect and ends the loop; network errors and other responses leave
// the next attempt eligible after its delay.
//
// The fixed attempt count does not impose a wall-clock deadline because each
// fetch can wait independently. Poll delays and the redirect timeout are not
// retained for unmount cleanup. Exhausting all probes leaves the completion view
// visible and publishes its translated timeout message for manual recovery.
async function waitForServiceRestart() {
  // Bound the number of probes and delay retries, while retaining the existing
  // fetch behavior without a per-request timeout or external cancellation signal.
  const maxAttempts = 60
  const interval = 1000

  // Allow the accepted installation to begin restarting before the first probe;
  // a subsequent non-ready response uses the shorter delay between retry attempts.
  await new Promise((resolve) => setTimeout(resolve, 3000))

  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    try {
      // Probe the setup endpoint because HTTP availability alone does not imply
      // normal application mode; restart transitions can also fail at the transport level.
      if (await probeSetupRestartReady()) {
        serviceReady.value = true
        // Leave the readiness message visible briefly before navigating to login;
        // the scheduled timeout retains the existing behavior after this function returns.
        setTimeout(() => {
          window.location.href = '/login'
        }, 1500)
        return
      }
    } catch {
      // Restart-time transport or decoding failures leave another probe eligible;
      // only exhausting the shared attempt budget publishes a final timeout message.
    }

    await new Promise((resolve) => setTimeout(resolve, interval))
  }

  // All permitted probes completed without a normal-mode response. Keep the
  // installation result visible while explaining that readiness was not observed.
  errorMessage.value = t('setup.status.timeout')
}
</script>
