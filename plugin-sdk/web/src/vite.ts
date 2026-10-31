import { fileURLToPath } from 'node:url'
import type { Plugin, UserConfig } from 'vite'
import { scopeSelectors } from './postcss-scope.js'

// vite.ts provides the build contract expected by the host's script loader.
// Production output contains one IIFE and one stylesheet, with Vue and the SDK
// resolved from host globals. Development servers replace only the SDK import
// with the local mock so plugin pages can be exercised without a backend.

/** PluginBuildOptions identifies the plugin and its source/output locations. */
export interface PluginBuildOptions {
  pluginId: string // Manifest plugin identifier.
  entry?: string // Source entry module path.
  outDir?: string // Frontend build output directory.
}

/** tokenrouterPlugin creates the shared Vite library build and development preset.
 * The consuming project adds its Vue SFC plugin. Output assets must remain
 * inside the supplied frontend directory so the package publisher can hash them.
 */
export function tokenrouterPlugin(options: PluginBuildOptions): UserConfig {
  return {
    define: {
      'process.env.NODE_ENV': '"production"',
      __TR_PLUGIN_DEV_ID__: JSON.stringify(options.pluginId),
    },
    plugins: [mockSdk()],
    css: { postcss: { plugins: [scopeSelectors(options.pluginId)] } },
    build: {
      outDir: options.outDir ?? '../dist/frontend',
      emptyOutDir: true,
      cssCodeSplit: false,
      lib: {
        entry: options.entry ?? 'src/index.ts',
        formats: ['iife'],
        name: '__TR_PLUGIN_ENTRY__',
        fileName: () => 'index.js',
        cssFileName: 'index',
      },
      rollupOptions: {
        external: ['vue', '@tokenrouter/plugin-sdk'],
        output: {
          globals: {
            vue: '__TR_VUE__',
            '@tokenrouter/plugin-sdk': '__TR_PLUGIN_SDK__',
          },
        },
      },
    },
  }
}

/** mockSdk redirects SDK imports only while Vite serves local development pages.
 * Production builds retain external imports for the host global. The resolved
 * path follows the installed SDK package rather than the consumer's working directory.
 */
function mockSdk(): Plugin {
  return {
    name: 'tokenrouter-development-host',
    apply: 'serve',
    enforce: 'pre',
    /** resolveId substitutes only the package's root SDK import during development.
     * Other imports return null for Vite's remaining resolvers. The replacement
     * path stays inside this installed package's compiled development module.
     */
    resolveId(source) {
      if (source === '@tokenrouter/plugin-sdk') {
        return fileURLToPath(new URL('./dev/index.js', import.meta.url))
      }
      return null
    },
  }
}
