import { defineConfig } from 'vitest/config'
import { resolve } from 'path'
import vue from '@vitejs/plugin-vue'

// vitest.config.ts configures component and utility tests for the frontend source
// tree. Vue files compile through the application plugin, shared setup supplies
// browser substitutes, and jsdom exposes DOM behavior while layout geometry is
// verified separately in browser checks.

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(import.meta.dirname, 'src'),
      // Match production source resolution so a clean checkout does not require an ignored SDK build.
      '@tokenflux/tokenrouter-plugin-sdk': resolve(
        import.meta.dirname,
        '../plugin-sdk/web/src/types.ts',
      ),
      'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js',
    },
  },
  test: {
    globals: true,
    clearMocks: false,
    environment: 'jsdom',
    setupFiles: ['./src/__tests__/setup.ts'],
    include: ['src/**/*.{test,spec}.{js,ts,jsx,tsx}'],
    exclude: ['node_modules', 'dist'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      include: ['src/**/*.{js,ts,vue}'],
      exclude: [
        'node_modules',
        'src/**/*.d.ts',
        'src/**/*.spec.ts',
        'src/**/*.test.ts',
        'src/main.ts',
      ],
      thresholds: {
        statements: 80,
        branches: 80,
        functions: 80,
        lines: 80,
      },
    },
  },
})
