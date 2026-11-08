import { defineConfig, loadEnv, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import checker from 'vite-plugin-checker'
import { resolve } from 'path'
// Native config loading requires source extensions; tsconfig.node.json rewrites
// this extension so emitted JavaScript resolves the compiled metadata module.
import { renderSocialMetadata } from './socialMetadata.ts'

// vite.config.ts configures development proxying and the browser assets embedded
// in release builds. Development documents receive public settings and branding
// before application startup. Vendor groups keep payment SDK initialization in
// the payment flow, while Vue and localization use their configured runtime builds.

// escapeHtml encodes HTML-sensitive characters in injected branding text and attributes.
function escapeHtml(value: string): string {
  return value.replace(
    /[&<>"']/g,
    (character) =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;',
      })[character] || character,
  )
}

// isSafeImageUrl recognizes the source syntax allowed for an injected site icon.
// Root-relative paths, image data URLs, and HTTP(S) URLs pass this filter. Callers
// use it for source selection; resource contents are outside this syntax check.
function isSafeImageUrl(value: string): boolean {
  const trimmed = value.trim()
  if ((trimmed.startsWith('/') && !trimmed.startsWith('//')) || /^data:image\//i.test(trimmed)) {
    return true
  }
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

// injectBranding replaces existing title and icon tags using nonempty site settings.
// Text and attribute characters are escaped before insertion, and icon replacement
// requires an allowed source URL. Missing tags or unavailable settings retain the
// corresponding markup from the supplied document template.
function injectBranding(
  html: string,
  config: {
    site_name?: string // Optional title branding text.
    site_logo?: string // Optional favicon source URL.
  },
): string {
  let brandedHtml = html
  const siteName = config.site_name?.trim()
  if (siteName) {
    brandedHtml = brandedHtml.replace(
      /<title>[^<]*<\/title>/i,
      `<title>${escapeHtml(siteName)} - AI API Gateway</title>`,
    )
  }

  const siteLogo = config.site_logo?.trim()
  if (siteLogo && isSafeImageUrl(siteLogo)) {
    brandedHtml = brandedHtml.replace(
      /<link\s+rel=["']icon["'][^>]*>/i,
      `<link rel="icon" href="${escapeHtml(siteLogo)}" />`,
    )
  }
  return brandedHtml
}

// injectPublicSettings creates the development document-transform plugin for public
// settings. Its pre-transform completes before the app reads injected branding and
// access flags. Unsuccessful or unavailable configuration leaves the template intact
// so browser startup can request public settings through its normal API path.
// devOrigin supplies the local sharing destination when frontend_url is blank.
function injectPublicSettings(backendUrl: string, devOrigin: string): Plugin {
  return {
    name: 'inject-public-settings',
    apply: 'serve',
    transformIndexHtml: {
      order: 'pre',
      // handler fetches startup configuration for one development document render.
      // Only a successful API envelope is injected. A request timeout, network
      // failure, or transform error leaves the original document for client loading.
      async handler(html, context) {
        try {
          // Bound configuration loading to two seconds so a disconnected backend
          // does not indefinitely delay serving the browser's application document.
          const response = await fetch(`${backendUrl}/api/v1/settings/public`, {
            signal: AbortSignal.timeout(2000),
          })
          if (response.ok) {
            const data = await response.json()
            if (data.code === 0 && data.data) {
              const script = `<script>window.__APP_CONFIG__=${JSON.stringify(data.data)};</script>`
              const metadata = renderSocialMetadata(
                data.data,
                new URL(context.path, devOrigin).href,
              )
              return injectBranding(html, data.data).replace(
                '</head>',
                `${metadata}\n${script}\n</head>`,
              )
            }
          }
        } catch (e) {
          // The browser can fetch public settings after startup, so a failed
          // development-time injection is logged and returns the unmodified template.
          console.warn(
            '[vite] Failed to fetch public settings, the browser will request them through the API:',
            (e as Error).message,
          )
        }
        return html
      },
    },
  }
}

export default defineConfig(({ mode }) => {
  // Development addresses come from the selected mode environment, with local server defaults for an unconfigured checkout.
  const env = loadEnv(mode, process.cwd(), '')
  const backendUrl = env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'
  const devPort = Number(env.VITE_DEV_PORT || 3000)

  return {
    plugins: [
      vue(),
      checker({
        vueTsc: true,
      }),
      injectPublicSettings(backendUrl, `http://localhost:${devPort}`),
    ],
    resolve: {
      alias: {
        '@': resolve(import.meta.dirname, 'src'),
        // Host code consumes the SDK's shared error class and constants directly;
        // published plugin bundles still externalize the full SDK to host globals.
        '@tokenflux/tokenrouter-plugin-sdk': resolve(
          import.meta.dirname,
          '../plugin-sdk/web/src/types.ts',
        ),
        // Vue I18n 11 enables CSP-compatible JIT compilation by default; keep
        // the runtime distribution used by the application and component tests.
        'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js',
      },
    },
    build: {
      outDir: '../backend/internal/web/dist',
      emptyOutDir: true,
      rolldownOptions: {
        output: {
          strictExecutionOrder: true,
          codeSplitting: {
            groups: [
              {
                test: /node_modules/,
                includeDependenciesRecursively: false,
                // name assigns vendor modules to bundles according to their runtime role.
                // Payment namespaces receive separate chunks so loading a shared
                // application dependency does not initialize a payment SDK early.
                name(id: string) {
                  if (id.includes('node_modules')) {
                    // Vue runtime, routing, state, and compiler packages share this vendor group.
                    if (
                      id.includes('/vue/') ||
                      id.includes('/vue-router/') ||
                      id.includes('/pinia/') ||
                      id.includes('/@vue/')
                    ) {
                      return 'vendor-vue'
                    }

                    // Reused browser utilities and spreadsheet exports share the UI vendor group.
                    if (id.includes('/@vueuse/') || id.includes('/xlsx/')) {
                      return 'vendor-ui'
                    }

                    // Chart rendering libraries share a group used by analytics pages.
                    if (id.includes('/chart.js/') || id.includes('/vue-chartjs/')) {
                      return 'vendor-chart'
                    }

                    // Localization packages remain together with their message compiler dependencies.
                    if (id.includes('/vue-i18n/') || id.includes('/@intlify/')) {
                      return 'vendor-i18n'
                    }

                    // Stripe initialization follows its dynamically imported payment entry point.
                    if (id.includes('/@stripe/stripe-js/')) {
                      return 'vendor-stripe'
                    }

                    // Airwallex prefetches payment scripts during initialization; keeping its
                    // namespace separate preserves the payment page as that load trigger.
                    if (id.includes('/@airwallex/')) {
                      return 'vendor-airwallex'
                    }

                    // Remaining vendor modules use the shared miscellaneous dependency group.
                    return 'vendor-misc'
                  }

                  return null
                },
              },
            ],
          },
        },
      },
    },
    server: {
      host: '0.0.0.0',
      port: devPort,
      proxy: {
        '/mcp': {
          target: backendUrl,
          changeOrigin: true,
        },
        '/api': {
          target: backendUrl,
          changeOrigin: true,
        },
        '/v1': {
          target: backendUrl,
          changeOrigin: true,
        },
        '/setup': {
          target: backendUrl,
          changeOrigin: true,
        },
      },
    },
  }
})
