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

// Style-rule nesting inherits its parent's root even through grouping at-rules.
// Top-level media rules still need a root, while nested selectors retain their
// descendant, relative, and parent-reference relationships without another prefix.
test('scopes only the outer style rule across CSS nesting and grouping at-rules', async () => {
  const result = await postcss([scopeSelectors('test.plugin')]).process(
    `
    .parent {
      .child { color: red }
      &:hover { color: blue }
      @media (width > 20rem) {
        & > .nested { color: green }
        @supports (display: grid) { .deep { display: grid } }
      }
    }
    @media (width > 30rem) { @supports (display: grid) { .outside { display: grid } } }
    @keyframes fade { from { opacity: 0 } to { opacity: 1 } }
    `,
    { from: undefined },
  )
  const selectors = []
  result.root.walkRules((rule) => selectors.push(rule.selector))
  assert.deepEqual(selectors, [
    '[data-tr-plugin="test.plugin"] .parent',
    '.child',
    '&:hover',
    '& > .nested',
    '.deep',
    '[data-tr-plugin="test.plugin"] .outside',
    'from',
    'to',
  ])
})

// Animation names may share spelling with easing, direction, and iteration keywords.
// Grammar matching must rename only the name position, preserving function arguments,
// quoted identifiers, multiple animations, and explicitly selected external names.
test('renames animation names without changing shorthand keywords or function arguments', async () => {
  const result = await postcss([scopeSelectors('test.plugin')]).process(
    `
    @keyframes linear { from { opacity: 0 } }
    @keyframes ease { from { opacity: 0 } }
    @keyframes reverse { from { opacity: 0 } }
    @keyframes infinite { from { opacity: 0 } }
    @keyframes end { from { opacity: 0 } }
    @keyframes fade { from { opacity: 0 } }
    @keyframes "spaced name" { from { opacity: 0 } }
    .a { animation: linear 1s linear }
    .b { animation: 1s ease reverse infinite fade }
    .c { animation: fade 1s steps(4, end) }
    .d { animation-name: linear, "spaced name", external }
    .e { -webkit-animation: fade 1s, external 2s ease }
    `,
    { from: undefined },
  )
  const values = []
  result.root.walkDecls(/animation/, (declaration) => values.push(declaration.value))
  assert.deepEqual(values, [
    'linear 1s test-plugin--linear',
    '1s ease reverse infinite test-plugin--fade',
    'test-plugin--fade 1s steps(4, end)',
    'test-plugin--linear, "test-plugin--spaced name", external',
    'test-plugin--fade 1s, external 2s ease',
  ])
})

// A literal name fixes animation identity even when duration or easing uses variables.
// The separate animation-name and duration form also remains usable, and an unrelated
// external animation in the same list does not acquire the local prefix.
test('preserves dynamic animation properties beside an unambiguous literal name', async () => {
  const result = await postcss([scopeSelectors('test.plugin')]).process(
    `
    @keyframes fade { from { opacity: 0 } }
    @keyframes linear { from { opacity: 0 } }
    .a { animation: fade var(--duration) ease }
    .b { animation: fade 1s steps(var(--steps), end) }
    .c { animation: var(--duration) "linear" }
    .d { animation: fade calc(1s + var(--delay)), external var(--duration) }
    .e { animation-name: linear; animation-duration: var(--duration) }
    `,
    { from: undefined },
  )
  const values = []
  result.root.walkDecls(/animation/, (declaration) => values.push(declaration.value))
  assert.deepEqual(values, [
    'test-plugin--fade var(--duration) ease',
    'test-plugin--fade 1s steps(var(--steps), end)',
    'var(--duration) "test-plugin--linear"',
    'test-plugin--fade calc(1s + var(--delay)), external var(--duration)',
    'test-plugin--linear',
    'var(--duration)',
  ])
})

// Runtime-selected names cannot be coordinated with renamed local keyframes.
// These ambiguous forms must fail with an actionable diagnostic instead of
// silently retaining references that no longer identify the emitted animation.
test('rejects dynamic local animation identities while preserving sheets without local keyframes', async () => {
  for (const declaration of [
    'animation: var(--motion)',
    'animation: var(--easing) linear',
    'animation-name: var(--name, fade)',
  ]) {
    await assert.rejects(
      postcss([scopeSelectors('test.plugin')]).process(
        `@keyframes fade { from { opacity: 0 } } .page { ${declaration} }`,
        { from: undefined },
      ),
      /literal animation-name and separate dynamic animation properties/,
    )
  }
  const result = await postcss([scopeSelectors('test.plugin')]).process(
    '.page { animation: var(--motion) }',
    { from: undefined },
  )
  assert.match(result.css, /animation: var\(--motion\)/)
})

// CSS escapes and quoted names encode the same identity in different forms.
// Prefixing must decode those names before lookup and emit a valid representation
// so the resulting keyframe and its reference still agree.
test('matches escaped keyframe names with their decoded references', async () => {
  const result = await postcss([scopeSelectors('test.plugin')]).process(
    String.raw`@keyframes f\61 de { from { opacity: 0 } } .page { animation-name: fade, f\61 de }`,
    { from: undefined },
  )
  assert.match(result.css, /@keyframes test-plugin--fade/)
  assert.match(result.css, /animation-name: test-plugin--fade, test-plugin--fade/)
})
