import { join, posix } from 'node:path'
import { pathToFileURL } from 'node:url'
import {
  collectSourceFiles,
  frontendRoot,
  parseSource,
  runSourceCheck,
  sourceLocation,
} from './source-analysis.mjs'

// check-imports.mjs checks frontend imports against the ownership of application
// layers and private page modules. Alias and relative imports are normalized to
// frontend-relative paths before comparison. Type imports follow the same layer
// rules, while API domain types may reference one another across API modules.

// @project-doc docs/architecture/frontend_modules.md#dependency_ownership
// PRIVATE_MODULES names implementation folders and their permitted page entry
// points; route files may load the provider entry component through its exact path.
export const PRIVATE_MODULES = [
  {
    directory: 'src/views/admin/plugins/',
    owners: [],
    entries: [
      'src/views/admin/plugins/PluginsView.vue',
      'src/views/admin/plugins/PluginDetailView.vue',
      'src/views/admin/plugins/PluginSourcesView.vue',
    ],
  },
  {
    directory: 'src/views/admin/providers/',
    owners: [],
    entries: ['src/views/admin/providers/ProvidersView.vue'],
  },
  {
    directory: 'src/views/admin/settings/',
    owners: ['src/views/admin/SettingsView.vue'],
    entries: [],
  },
  {
    directory: 'src/views/user/payment/',
    owners: ['src/views/user/PaymentView.vue', 'src/views/user/PaymentResultView.vue'],
    entries: [],
  },
  {
    directory: 'src/views/admin/risk-control/',
    owners: [
      'src/views/admin/RiskControlView.vue',
      'src/views/admin/__tests__/RiskControlView.spec.ts',
    ],
    entries: [],
  },
]

// IMPORT_EXCEPTIONS records exact importer/target pairs when a documented shared
// interface must cross a private module's ownership rule. Each exception permits
// only its named importer and target, leaving neighboring dependencies checked.
export const IMPORT_EXCEPTIONS = [
  // The import integration test mounts the actual provider dialog to verify the
  // cross-module import workflow; application modules still use the owning page.
  {
    importer: 'src/__tests__/integration/data-import.spec.ts',
    target: 'src/views/admin/providers/operations/ImportDataModal.vue',
  },
  // The type-guard regression exercises the provider protocol decoder directly;
  // this test dependency does not expose that decoder to application consumers.
  {
    importer: 'src/utils/__tests__/typesafe.spec.ts',
    target: 'src/views/admin/providers/testing/providerTestProtocols',
  },
]

/**
 * localTarget resolves source aliases and relative imports into frontend paths.
 * Package imports return null because their ownership is defined by package
 * dependencies. Extensionless paths remain extensionless for directory-level rules.
 */
function localTarget(importer, specifier) {
  if (specifier.startsWith('@/')) return posix.normalize(`src/${specifier.slice(2)}`)
  if (specifier.startsWith('.'))
    return posix.normalize(posix.join(posix.dirname(importer), specifier))
  return null
}

/**
 * importViolation returns the violated ownership rule, or null for allowed imports.
 * Private directories admit their own files and the declared owner entry points.
 * Router modules may import an exact declared page entry while its implementation
 * remains private to that page's directory.
 */
export function importViolation(importer, target) {
  if (
    IMPORT_EXCEPTIONS.some(
      (exception) => exception.importer === importer && exception.target === target,
    )
  )
    return null
  if (
    importer.startsWith('src/api/') &&
    /^(?:src\/(?:components|views|stores|router|composables|pluginHost))(?:\/|$)/.test(target)
  ) {
    return 'API modules cannot import UI, store, router, composable, or plugin host modules.'
  }
  if (importer.startsWith('src/components/') && /^src\/(?:views|pluginHost)(?:\/|$)/.test(target)) {
    return 'Components cannot import page or plugin host implementations.'
  }
  if (importer.startsWith('src/composables/') && /^src\/components(?:\/|$)/.test(target)) {
    return 'Shared composables cannot import component implementations.'
  }
  if (importer.startsWith('src/pluginHost/') && /^src\/views(?:\/|$)/.test(target)) {
    return 'The plugin host cannot import page implementations.'
  }
  for (const module of PRIVATE_MODULES) {
    if (
      !(target.startsWith(module.directory) || target === module.directory.slice(0, -1)) ||
      importer.startsWith(module.directory) ||
      module.owners.includes(importer)
    )
      continue
    if (
      (importer === 'src/router/index.ts' || importer.startsWith('src/router/')) &&
      module.entries.includes(target)
    )
      continue
    return `Import private module ${module.directory} through its owning page.`
  }
  return null
}

/** checkImports reports forbidden static dependencies from parsed module references. */
export function checkImports(text, filename) {
  return parseSource(text, filename).imports.flatMap((reference) => {
    const target = localTarget(filename, reference.value)
    const message = target && importViolation(filename, target)
    return message
      ? [{ ...sourceLocation(text, reference.offset), message: `${message} (${reference.value})` }]
      : []
  })
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exitCode = runSourceCheck(
    'check:imports',
    checkImports,
    collectSourceFiles(join(frontendRoot, 'src')),
  )
}
