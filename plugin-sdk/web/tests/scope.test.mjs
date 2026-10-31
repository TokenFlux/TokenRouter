import assert from 'node:assert/strict'
import test from 'node:test'
import postcss from 'postcss'
import { scopeSelectors } from '../dist/postcss-scope.js'

// scope.test.mjs checks the generated CSS boundary independently of the browser.
// Fixtures include nested rules, document roots, and quoted animation names so
// selector scoping cannot accidentally rewrite keyframe steps or lose references.

// Every ordinary selector is attached to the plugin root, including media-query
// descendants. Keyframe steps keep their syntax while definitions and references
// receive the same plugin-specific animation identity.
test('scopes selectors and animation references without rewriting keyframe steps', async () => {
  const result = await postcss([scopeSelectors('tokenflux.checkin')]).process(
    `
    :root { color: red }
    .card, button:hover { animation: pulse 1s ease }
    @media (min-width: 20rem) { .card > span { color: blue } }
    @keyframes pulse { from { opacity: 0 } to { opacity: 1 } }
    @keyframes "quoted" { 0% { opacity: 0 } 100% { opacity: 1 } }
    .quoted { animation-name: "quoted" }
  `,
    { from: undefined },
  )
  const selectors = []
  result.root.walkRules((rule) => {
    if (rule.parent?.type !== 'atrule' || !rule.parent.name.endsWith('keyframes')) {
      selectors.push(...rule.selectors)
    }
  })
  assert.ok(
    selectors.every((selector) =>
      selector.trim().startsWith('[data-tr-plugin="tokenflux.checkin"]'),
    ),
  )
  assert.match(result.css, /animation: tokenflux-checkin--pulse 1s ease/)
  assert.match(result.css, /animation-name: "tokenflux-checkin--quoted"/)
  assert.match(result.css, /from \{ opacity: 0 \}/)
})

// Build configuration cannot inject selector punctuation through the plugin ID.
// Invalid identities must fail before any CSS transformation or output publication.
test('rejects invalid plugin identifiers', () => {
  assert.throws(() => scopeSelectors('tokenflux.checkin"] body'), /Invalid/)
})
