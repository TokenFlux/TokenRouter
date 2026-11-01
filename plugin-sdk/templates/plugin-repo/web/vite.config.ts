import { defineConfig, mergeConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { tokenrouterPlugin } from '@tokenflux/tokenrouter-plugin-sdk/vite'

// vite.config.ts builds the example with the same preset used by third-party
// plugin authors. Vue SFC compilation stays in the example project, while the
// SDK preset owns global externals, CSS scoping, and stable output filenames.

export default defineConfig(
  mergeConfig(
    tokenrouterPlugin({
      pluginId: 'example.checkin',
      outDir: '../dist/frontend',
    }),
    { plugins: [vue()] },
  ),
)
