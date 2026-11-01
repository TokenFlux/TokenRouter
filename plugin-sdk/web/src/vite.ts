import { fileURLToPath } from 'node:url'
import type { Plugin, UserConfig } from 'vite'
import { scopeSelectors } from './postcss-scope.js'

// vite.ts provides the build contract expected by the host's script loader.
// Production output contains one IIFE and one stylesheet, with Vue and the SDK
// resolved from host globals. Plugin identity also selects the stylesheet scope
// and the local development host, keeping both entry points tied to the same
// consuming project instead of the SDK package's own build location.
//
// Development servers resolve the SDK root and mock configuration entry to one
// module so page requests share the configured fixtures. Both imports bypass
// dependency prebundling, which would otherwise create a second copy of the
// mock's mutable state for ordinary npm installations. Production builds retain
// their external globals and never include the development host implementation.

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
        external: ['vue', '@tokenflux/tokenrouter-plugin-sdk'],
        output: {
          globals: {
            vue: '__TR_VUE__',
            '@tokenflux/tokenrouter-plugin-sdk': '__TR_PLUGIN_SDK__',
          },
        },
      },
    },
  }
}

/** mockSdk gives development pages and fixture configuration the same module instance.
 * Production builds retain external imports for the host global. The resolved
 * path follows the installed SDK package rather than the consumer's working directory.
 *
 * An installed package normally participates in Vite dependency prebundling.
 * Excluding both public entry names prevents the explicit dev import from
 * receiving an optimized copy while the redirected root uses the original file.
 * Their shared state therefore survives both import forms within one development page.
 */
function mockSdk(): Plugin {
  return {
    name: 'tokenrouter-development-host',
    apply: 'serve',
    enforce: 'pre',
    /** config keeps both mock entry names outside Vite's dependency optimizer. */
    config() {
      return {
        optimizeDeps: {
          exclude: ['@tokenflux/tokenrouter-plugin-sdk', '@tokenflux/tokenrouter-plugin-sdk/dev'],
        },
      }
    },
    /** resolveId substitutes both public mock entries with one installed module.
     * Other imports return null for Vite's remaining resolvers. Page registration
     * and configureMock therefore read and update the same module-owned request handler.
     */
    resolveId(source) {
      if (
        source === '@tokenflux/tokenrouter-plugin-sdk' ||
        source === '@tokenflux/tokenrouter-plugin-sdk/dev'
      ) {
        return fileURLToPath(new URL('./dev/index.js', import.meta.url))
      }
      return null
    },
  }
}
