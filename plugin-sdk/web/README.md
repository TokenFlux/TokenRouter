# TokenRouter frontend plugin SDK

The package defines the frontend contract, the Vite library preset, scoped CSS,
and a local development mock. Production plugins use the host's Vue runtime
and UI wrappers. The SDK and examples use the MIT license.

```ts
import { defineConfig, mergeConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { tokenrouterPlugin } from '@tokenrouter/plugin-sdk/vite'

export default defineConfig(mergeConfig(
  tokenrouterPlugin({ pluginId: 'publisher.plugin' }),
  { plugins: [vue()] },
))
```

Register page components synchronously with `definePlugin`. The manifest's page
IDs must match the registered keys. `useHost()` is available during page setup;
it provides scoped HTTP requests, navigation, account refresh, notifications,
and confirmation. HTTP requests use plugin-relative paths beginning with `/`.

The production output is `index.js` plus `index.css` when the plugin imports
styles. Vue and the SDK remain external globals. CSS selectors receive the
plugin's `data-tr-plugin` prefix, and keyframe identifiers are namespaced.
Use the `--tr-color-*` variables exported in `CSS_VARIABLES`, plus the host's
existing `--radius-*` and `--z-*` variables, to follow light and dark themes.

`vite` uses the local mock for SDK imports. The mock supports the example's
check-in endpoints; authors can replace responses with `configureMock` from
`@tokenrouter/plugin-sdk/dev`. Unknown endpoints fail explicitly. Mock controls
use native elements and do not prove production styling, permissions, storage,
or billing behavior. The mock is excluded from production IIFE bundles.

Run `pnpm build`, `pnpm typecheck`, and `pnpm test` in this directory. The
check-in example under `plugin-sdk/go/examples/checkin/web` verifies that the
preset builds Vue pages without embedding a second Vue runtime.
