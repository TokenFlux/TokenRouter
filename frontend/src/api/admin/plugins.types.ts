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
  index_name?: string // Registered source display name.
  index_builtin?: boolean // Source is the host registration.
  signature_status?: PluginSignature // Latest source verification outcome.
  update_version?: string // Compatible newer offered version.
  current_version_yanked?: boolean // Installed release has been withdrawn.
  current_yank_reason?: string // Source-provided release withdrawal explanation.
  delisted?: boolean // Source withdrew the plugin listing.
  manifest: PluginManifest // Accepted installed release declaration.
  manifest_sha256: string // Original manifest content digest.
  enabled: boolean // Desired contribution availability flag.
  granted_permissions: string[] // Administrator-approved host capability names.
  limits: Partial<PluginLimits> // Stored installation-level capability policy.
  previous_version?: string // Retained rollback release version.
  ignore_host_version?: boolean // Accepted gateway version exception.
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
  ignore_host_version?: boolean // Skip minimum and maximum gateway versions.
  force?: boolean // Replace the same installed plugin ID.
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

/** PluginSignature distinguishes source verification observations from accepted cache timestamps. */
export type PluginSignature = 'unknown' | 'official' | 'custom' | 'unsigned' | 'invalid'

/** PluginIndex describes a registered source without returning its accepted catalog bytes. */
export interface PluginIndex {
  id: number // Durable source registration identifier.
  name: string // Administrator-selected source display name.
  url: string // HTTPS catalog document location.
  public_key: string // Optional pinned base64 Ed25519 key.
  builtin: boolean // Host-configured official source registration.
  enabled: boolean // Source contributes to market selection.
  signature_status: PluginSignature // Latest signature verification observation.
  cached_at: string | null // Last accepted document publication timestamp.
  last_error: string // Bounded latest source refresh failure.
  refreshing: boolean // Source transfer is currently pending.
}

/** PluginIndexInput changes supplied source fields and preserves omitted stored values. */
export interface PluginIndexInput {
  name?: string // Required display name during creation.
  url?: string // Required HTTPS location during creation.
  /** public_key omission keeps the stored pin; an explicit empty string clears that trust requirement. */
  public_key?: string
  enabled?: boolean // Optional desired source availability flag.
}

/** PluginCatalogVersion combines immutable release metadata and current host admission decisions. */
export interface PluginCatalogVersion {
  version: string // Published release semantic version.
  publishedAt: string // Publisher release creation timestamp.
  prerelease: boolean // Requires explicit prerelease visibility selection.
  yanked: boolean // Source withdrew this specific release.
  yankReason?: string // Published reason for release withdrawal.
  changelog: string // Bounded publisher release notes Markdown.
  manifest: PluginManifest // Complete reviewed release declaration.
  manifestSha256: string // Original release manifest asset digest.
  artifacts: Record<string, PluginArchive> // Accepted archives by target platform.
  compatible: boolean // Host satisfies all declared requirements.
  incompatibilities: PluginIncompatibility[] // Independent host requirement failures.
  installable: boolean // Compatibility, withdrawal, and ownership allow selection.
}

/** PluginCatalogEntry retains source identity across duplicate plugin names and identifiers. */
export interface PluginCatalogEntry {
  host_platform: string // Current host archive target.
  index_id: number // Owning index source identifier.
  index_name: string // Registered source display name.
  builtin: boolean // Source is the built-in registration.
  signature_status: PluginSignature // Latest source verification observation.
  id: string // Publisher-qualified plugin installation identity.
  repository: string // Registered public HTTPS repository.
  categories: string[] // Registered discovery category names.
  icon_url?: string // Resolved HTTPS presentation icon.
  manifest: PluginManifest // Stable or retained presentation declaration.
  versions: PluginCatalogVersion[] // Offered and withdrawn retained releases.
  delisted: boolean // Source withdrew this plugin listing.
  recommended_version: string // Highest compatible offered stable version.
  installed_version?: string // Accepted local installed release version.
  installed_from_source: boolean // This source owns the accepted installation.
  installed_from_other_source: boolean // Another source owns this plugin identity.
  current_version_yanked: boolean // Source withdrew the installed release.
  current_yank_reason?: string // Installed release withdrawal explanation.
  update_version?: string // Newer compatible recommended stable version.
}

/** PluginUpdateInput selects one reviewed indexed version or uploaded replacement archive. */
export interface PluginUpdateInput {
  version?: string // Explicit indexed target semantic version.
  upload_token?: string // Accepted uploaded target package handle.
  accept_permissions: string[] // Complete administrator-confirmed target capability set.
  ignore_host_version?: boolean // Skip minimum and maximum gateway versions.
  force?: boolean // Permit replacement at the same version.
}

/** PluginArchive binds a release target to accepted compressed archive bytes. */
export interface PluginArchive {
  url: string // HTTPS archive download location.
  sha256: string // Accepted compressed archive SHA-256.
  size: number // Accepted compressed archive byte count.
}

/** PluginIncompatibility explains one independent requirement the current host cannot satisfy. */
export interface PluginIncompatibility {
  code: string // Stable host requirement failure code.
  detail: string // Bounded requirement failure explanation.
}
