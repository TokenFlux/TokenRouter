import type { Document, Plugin, Rule } from 'postcss'
import selectorParser from 'postcss-selector-parser'
import valueParser from 'postcss-value-parser'

// postcss-scope.ts confines plugin selectors to their mounted root and renames
// animation identifiers to avoid collisions between independently built plugins.
// Keyframe step selectors retain their CSS meaning, while ordinary rules under
// media or support queries receive the same plugin attribute prefix.
//
// Scope transformation runs once after the stylesheet has been assembled.
// Keyframe definitions are collected before animation declarations are rewritten,
// so references work independently of source order. The plugin identifier must
// satisfy the manifest grammar before it is inserted into a selector or an
// animation name, preventing malformed CSS from build configuration mistakes.

/** scopeSelectors returns a PostCSS plugin for one validated plugin identifier.
 * Bare document-root selectors become the mounted root; ordinary selectors are
 * prefixed with its attribute. This is style isolation, not a security sandbox.
 *
 * Keyframe steps are excluded from selector rewriting because from, to, and
 * percentage selectors belong to animation syntax. Keyframe names and references
 * change together, preserving their identity across independently parsed CSS
 * declarations. The transformation never permits a global escape selector.
 */
export function scopeSelectors(pluginId: string): Plugin {
  if (!/^[a-z][a-z0-9-]{1,31}\.[a-z][a-z0-9-]{1,47}$/.test(pluginId)) {
    throw new Error('Invalid TokenRouter plugin identifier')
  }
  const scope = `[data-tr-plugin="${pluginId}"]`
  return {
    postcssPlugin: 'tokenrouter-plugin-scope',
    /** OnceExit scopes the completed stylesheet and preserves animation references.
     * Definitions are collected before declarations are rewritten, so an animation
     * can be referenced earlier than its keyframe rule in the same stylesheet.
     *
     * Ordinary rules receive the plugin root, while keyframe step selectors retain
     * their grammar. References are renamed only for definitions found in this
     * stylesheet; authors should keep reusable animation definitions and their
     * references together when separate files pass through independent CSS transforms.
     */
    OnceExit(root) {
      const animations = new Map<string, string>()
      root.walkAtRules(/^(?:-webkit-)?keyframes$/, (rule) => {
        const parsed = valueParser(rule.params)
        const name = parsed.nodes.find((node) => node.type === 'word' || node.type === 'string')
        if (!name || (name.type !== 'word' && name.type !== 'string')) {
          throw rule.error('Keyframes require an animation name')
        }
        const original = name.value
        const renamed = `${pluginId.replace('.', '-')}--${original}`
        animations.set(original, renamed)
        name.value = renamed
        rule.params = parsed.toString()
      })
      root.walkRules((rule) => {
        if (insideKeyframes(rule)) return
        rule.selector = selectorParser((selectors) => {
          selectors.each((selector) => {
            const first = selector.first
            const isRoot =
              first &&
              ((first.type === 'tag' && (first.value === 'html' || first.value === 'body')) ||
                (first.type === 'pseudo' && first.value === ':root'))
            const prefix = selectorParser().astSync(scope).first!.nodes[0]!.clone()
            if (isRoot) {
              first.replaceWith(prefix)
            } else {
              selector.prepend(selectorParser.combinator({ value: ' ' }))
              selector.prepend(prefix)
            }
          })
        }).processSync(rule.selector)
      })
      root.walkDecls(/^(?:-webkit-)?animation(?:-name)?$/, (declaration) => {
        const parsed = valueParser(declaration.value)
        parsed.walk((node) => {
          if (node.type === 'word' || node.type === 'string') {
            const renamed = animations.get(node.value)
            if (renamed) node.value = renamed
          }
        })
        declaration.value = parsed.toString()
      })
    },
  }
}

/** insideKeyframes reports whether a rule belongs to an animation definition. */
function insideKeyframes(rule: Rule): boolean {
  let parent: Rule['parent'] | Document = rule.parent
  while (parent) {
    if (parent.type === 'atrule' && /^(?:-webkit-)?keyframes$/.test(parent.name)) return true
    parent = parent.parent
  }
  return false
}
