import type {
  PluginInstallation,
  PluginLimits,
  PluginCatalogEntry,
  PluginIndex,
} from '@/api/admin/plugins.types'

// fixtures.ts creates independent installation and quota values for administrator
// component tests. The defaults mirror the resolved backend policy, while each
// call returns fresh nested objects and plan arrays so one editor's changes
// cannot alter another test's starting state.

/** limitsFixture returns independently mutable resolved limits for every capability group. */
export function limitsFixture(): PluginLimits {
  return {
    wallet: {
      max_credit_per_op: 1,
      max_credit_per_user_per_day: 5,
      max_credit_per_day: 200,
      max_debit_per_op: 100,
    },
    redeem: {
      max_balance_per_code: 5,
      max_balance_value_per_day: 200,
      max_concurrency_per_code: 5,
      max_codes_per_day: 200,
      allowed_plan_ids: [],
      max_subscription_days_per_code: 31,
    },
    subscription: {
      allowed_plan_ids: [],
      max_days_per_grant: 31,
      max_grants_per_user_per_day: 1,
      max_grants_per_day: 50,
    },
    email: { max_per_user_per_day: 5, max_per_day: 500 },
  }
}

/** installationFixture returns a running upload with one editable reward and wallet grant. */
export function installationFixture(): PluginInstallation {
  return {
    plugin_id: 'test.configuration',
    version: '1.0.0',
    source: 'upload',
    index_id: null,
    manifest: {
      schemaVersion: 1,
      id: 'test.configuration',
      version: '1.0.0',
      apiVersion: 1,
      minHostVersion: '0.6.0',
      name: { en: 'Configuration' },
      description: { en: 'Configuration fixture' },
      publisher: { name: 'Tests' },
      license: 'MIT',
      permissions: ['wallet.credit'],
      config: {
        fields: [
          {
            key: 'reward',
            type: 'number',
            label: { en: 'Reward' },
            required: true,
            min: 0,
            max: 100,
          },
        ],
      },
    },
    manifest_sha256: 'a'.repeat(64),
    enabled: true,
    granted_permissions: ['wallet.credit'],
    limits: {},
    status: {
      state: 'running',
      pid: 123,
      rss_bytes: 0,
      cpu_percent: 0,
      restarts: 0,
      since: '2026-11-01T00:00:00Z',
    },
    installed_at: '2026-11-01T00:00:00Z',
    updated_at: '2026-11-01T00:00:00Z',
  }
}

/** indexFixture returns a registered custom source with an accepted signed cache. */
export function indexFixture(): PluginIndex {
  return {
    id: 7,
    name: 'Fixture index',
    url: 'https://example.invalid/index.json',
    public_key: '',
    builtin: false,
    enabled: true,
    signature_status: 'custom',
    cached_at: '2026-11-01T00:00:00Z',
    last_error: '',
    refreshing: false,
  }
}

/** catalogFixture creates two offered releases with distinct permissions and a selected source. */
export function catalogFixture(): PluginCatalogEntry {
  const installation = installationFixture()
  return {
    host_platform: 'darwin/arm64',
    index_id: 7,
    index_name: 'Fixture index',
    builtin: false,
    signature_status: 'custom',
    id: installation.plugin_id,
    repository: 'https://github.com/example/plugin',
    categories: ['tools'],
    manifest: installation.manifest,
    delisted: false,
    recommended_version: '2.0.0',
    installed_from_source: false,
    installed_from_other_source: false,
    current_version_yanked: false,
    versions: ['2.0.0', '1.0.0'].map((version) => ({
      version,
      publishedAt: '2026-11-01T00:00:00Z',
      prerelease: false,
      yanked: false,
      changelog: '<img src=x onerror="alert(1)"><script>alert(2)</script>Release notes',
      manifest: {
        ...installation.manifest,
        version,
        permissions: version === '2.0.0' ? ['wallet.credit', 'wallet.debit'] : ['wallet.credit'],
      },
      manifestSha256: 'a'.repeat(64),
      artifacts: {
        'darwin/arm64': {
          url: 'https://example.invalid/package.tar.gz',
          sha256: 'b'.repeat(64),
          size: 100,
        },
      },
      compatible: true,
      incompatibilities: [],
      installable: true,
    })),
  }
}
