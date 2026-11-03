# TokenRouter frontend plugin SDK

The package defines the frontend contract, the Vite library preset, scoped CSS,
and a local development mock. Production plugins use the host's Vue runtime
and UI wrappers. The SDK and examples use the MIT license.

```ts
import { defineConfig, mergeConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { tokenrouterPlugin } from '@tokenflux/tokenrouter-plugin-sdk/vite'

export default defineConfig(mergeConfig(
  tokenrouterPlugin({ pluginId: 'publisher.plugin' }),
  { plugins: [vue()] },
))
```

Register page components synchronously with `definePlugin`. The manifest's page
IDs must match the registered keys. Pages can be object components, functional
components, or Promise loaders. `useHost()` is available during page setup or
synchronous functional rendering; it provides scoped HTTP requests, navigation,
account refresh, notifications, and confirmation. HTTP requests use plugin-relative
paths beginning with `/`.

The production output is `index.js` plus `index.css` when the plugin imports
styles. Vue and the SDK remain external globals. CSS selectors receive the
plugin's `data-tr-plugin` prefix, and keyframe identifiers are namespaced.
Nested CSS rules inherit the outer rule's scope, including through nested media
and support queries. Animation shorthand names are parsed separately from easing,
direction, and other animation properties.

Keep animation names literal when the stylesheet defines local keyframes.
`animation: fade var(--duration) ease` works because `fade` identifies the animation.
Such variables must supply non-name components, not extra comma-separated animations.
For a name that is also a shorthand keyword, use a quoted name or separate
`animation-name: linear; animation-duration: var(--duration)`. Runtime-selected
names such as `animation: var(--motion)` or `animation-name: var(--name)` fail the
build when local keyframes are present: their eventual values cannot be updated
to the generated names. Stylesheets without local keyframes retain those values.

Use the `--tr-color-*` variables exported in `CSS_VARIABLES`, plus the host's
existing `--radius-*` and `--z-*` variables, to follow light and dark themes.
`--tr-color-brand` supplies the primary button fill. Use `--tr-color-accent` for
links, selected tabs, and selection indicators: it follows `primary-600` in light
mode and `primary-500` in dark mode. Hosts predating this token need a fallback
that follows the reactive `host.theme` value.

`vite` uses the local mock for SDK imports. The mock supports the example's
check-in endpoints; authors can replace responses with `configureMock` from
`@tokenflux/tokenrouter-plugin-sdk/dev`. Unknown endpoints fail explicitly. The mock
shares one fixture state across the root SDK and `/dev` imports in installed packages.
The preset excludes both entries from dependency prebundling to preserve that state.
Mock controls use native elements and do not prove production styling, permissions,
storage, or billing behavior. The mock is excluded from production IIFE bundles.

Run `pnpm build`, `pnpm typecheck`, and `pnpm test` in this directory. Minimal
consumer fixtures under `tests/fixtures/consumer` check generated declarations,
Vue SFCs, lazy pages, and scoped output without embedding another Vue runtime.
These fixtures are excluded from the npm package. Complete plugins are maintained
in the [official template](https://github.com/TokenFlux/tokenrouter-plugin-template)
and [check-in repository](https://github.com/TokenFlux/tr-plugin-checkin).
