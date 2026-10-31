import type { PluginManifest } from '@/api/plugins.types'

// plugins.types.ts defines administrator installation, configuration, and quota
// responses. Secret values arrive only as presence markers; ordinary settings
// retain their manifest types. Process observations are transient, while accepted
// versions, grants, and limits belong to the saved installation.

/** PluginState lists the host's native and frontend-only lifecycle observations. */
export type PluginState =
  'stopped' | 'starting' | 'running' | 'backoff' | 'draining' | 'crashloop' | 'failed' | 'static'

/** PluginStatus contains the most recent local process observation. */
export interface PluginStatus {
  state: PluginState // Current local lifecycle state.
  pid: number // Native operating-system process identifier.
  rss_bytes: number // Last sampled resident bytes.
  cpu_percent: number // Last sampled CPU utilization.
  restarts: number // Abnormal restart count since reset.
  last_error?: string // Latest runtime failure description.
  since: string // Current state's start timestamp.
}

/** PluginTaskResult contains one completed native task's in-memory outcome. */
export interface PluginTaskResult {
  version: string // Process version used for execution.
  scheduled_at: string // Selected scheduled occurrence timestamp.
  started_at: string // Actual host execution start.
  duration_ms: number // Observed execution duration in milliseconds.
  error?: string // Bounded task failure description.
}

/** PluginTaskStatus combines future scheduling with the latest completed result. */
export interface PluginTaskStatus {
  name: string // Manifest-local scheduled task name.
  next_run_at: string // Next occurrence or zero timestamp.
  running: boolean // Host invocation remains in flight.
  last_run?: PluginTaskResult // Latest completed in-memory execution.
}

/** PluginInstallation exposes persisted administrative intent without secret values or paths. */
export interface PluginInstallation {
  plugin_id: string // Publisher-qualified plugin installation identity.
  version: string // Accepted installed release version.
  source: 'index' | 'upload' | 'dev' // Accepted package distribution source.
  index_id: number | null // Optional distribution index identifier.
  manifest: PluginManifest // Accepted installed release declaration.
  manifest_sha256: string // Original manifest content digest.
  enabled: boolean // Desired contribution availability flag.
  granted_permissions: string[] // Administrator-approved host capability names.
  limits: Partial<PluginLimits> // Stored installation-level capability policy.
  previous_version?: string // Retained rollback release version.
  status: PluginStatus // Current local process observation.
  tasks?: PluginTaskStatus[] // Registered native task observations.
  installed_at: string // First installation creation timestamp.
  updated_at: string // Latest persisted modification timestamp.
}

/** PluginChange reports installation persistence and any remaining configuration requirement. */
export interface PluginChange {
  installation: PluginInstallation // Accepted installation and runtime state.
  needs_configuration: boolean // Required fields still need values.
  repaired: boolean // Missing package files were restored.
}

/** PluginUploadPreview identifies a validated archive awaiting an administrator's install decision. */
export interface PluginUploadPreview {
  upload_token: string // Expiring staged archive reference.
  expires_at: string // Staging token expiration timestamp.
  manifest: PluginManifest // Validated uploaded release declaration.
  manifest_sha256: string // Original manifest content digest.
  platform: string // Verified target package platform.
}

/** PluginInstallInput selects one distribution source and confirms the requested capabilities. */
export interface PluginInstallInput {
  source: 'upload' | 'index' // Selected package distribution source.
  upload_token?: string // Staged upload reference when uploading.
  index_id?: number // Selected distribution index identifier.
  plugin_id?: string // Catalog-selected plugin installation identity.
  version?: string // Catalog-selected release semantic version.
  accept_permissions: string[] // Explicitly confirmed host capability names.
  enable: boolean // Start after required configuration exists.
}

/** PluginSecretState reports existence without returning the stored credential. */
export interface PluginSecretState {
  set: boolean // Encrypted field value exists.
}

/** PluginConfigValue retains an ordinary field value or a redacted secret marker. */
export type PluginConfigValue = string | number | boolean | PluginSecretState

/** PluginConfig wraps editable values under the endpoint's redacted configuration boundary. */
export interface PluginConfig {
  values: Record<string, PluginConfigValue> // Ordinary values and secret-presence markers.
}

/** PluginLimits contains fully resolved spending, entitlement, and email allowances. */
export interface PluginLimits {
  wallet: PluginWalletLimits // Direct balance adjustment allowances.
  redeem: PluginRedeemLimits // Single-use credential issuance allowances.
  subscription: PluginSubscriptionLimits // Direct subscription issuance allowances.
  email: PluginEmailLimits // Optional message delivery allowances.
}

/** PluginWalletLimits caps direct credits and each caller-authorized debit. */
export interface PluginWalletLimits {
  max_credit_per_op: number // Maximum credit amount per operation.
  max_credit_per_user_per_day: number // Daily credit allowance per recipient.
  max_credit_per_day: number // Daily aggregate credit amount allowance.
  max_debit_per_op: number // Maximum debit amount per operation.
}

/** PluginRedeemLimits caps face values, counts, plans, and frozen subscription days. */
export interface PluginRedeemLimits {
  max_balance_per_code: number // Maximum balance face value per code.
  max_balance_value_per_day: number // Daily balance-code face value allowance.
  max_concurrency_per_code: number // Maximum concurrent slots per code.
  max_codes_per_day: number // Daily aggregate code issuance count.
  allowed_plan_ids: number[] // Allowed subscription plan identity list.
  max_subscription_days_per_code: number // Maximum frozen subscription day count.
}

/** PluginSubscriptionLimits controls direct subscription grants independently of issued codes. */
export interface PluginSubscriptionLimits {
  allowed_plan_ids: number[] // Allowed subscription plan identity list.
  max_days_per_grant: number // Maximum issued duration in days.
  max_grants_per_user_per_day: number // Daily grant count per recipient.
  max_grants_per_day: number // Daily aggregate grant count allowance.
}

/** PluginEmailLimits caps all accepted claims, including skipped or uncertain outcomes. */
export interface PluginEmailLimits {
  max_per_user_per_day: number // Daily delivery claims per recipient.
  max_per_day: number // Daily aggregate delivery claim count.
}

/** PluginLogLine contains one bounded retained process diagnostic. */
export interface PluginLogLine {
  time: string // Host observation timestamp string.
  level: string // Normalized process log severity.
  message: string // Bounded process diagnostic message.
}
