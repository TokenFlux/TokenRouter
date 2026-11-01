import { find, generate, ident, lexer, List, parse, walk } from 'css-tree'
import type { CssNode, Identifier, StringNode, Value } from 'css-tree'
import type { Declaration, Document, Plugin, Rule } from 'postcss'
import selectorParser from 'postcss-selector-parser'

// postcss-scope.ts confines plugin selectors to their mounted root and renames
// animation identifiers to avoid collisions between independently built plugins.
// Keyframe step selectors retain their CSS meaning, while ordinary rules under
// media or support queries receive the same plugin attribute prefix. Nested
// style rules retain their relative selectors beneath that already scoped rule.
//
// Scope transformation runs once after the stylesheet has been assembled.
// Keyframe definitions are collected before animation declarations are rewritten,
// so references work independently of source order. The plugin identifier must
// satisfy the manifest grammar before it is inserted into a selector or an
// animation name, preventing malformed CSS from build configuration mistakes.
// Animation grammar identifies names separately from easing and other shorthand
// fields. Dynamic substitutions require an unambiguous literal name; otherwise
// the build fails because runtime custom properties cannot be safely renamed.

/** AnimationName identifies a literal CSS animation name without unrelated value tokens. */
type AnimationName = Identifier | StringNode

/** scopeSelectors returns a PostCSS plugin for one validated plugin identifier.
 * Bare document-root selectors become the mounted root; ordinary selectors are
 * prefixed with its attribute. This is style isolation, not a security sandbox.
 *
 * Keyframe steps are excluded from selector rewriting because from, to, and
 * percentage selectors belong to animation syntax. Keyframe names and references
 * change together, preserving their identity across independently parsed CSS
 * declarations. Dynamic animation names cannot preserve that association and
 * must be expressed with a literal animation-name and separate dynamic properties.
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
        const parsed = parse(rule.params, { context: 'value' })
        const name = parsed.type === 'Value' ? parsed.children.first : null
        if (!isAnimationName(name) || lexer.matchType('keyframes-name', parsed).error) {
          throw rule.error('Keyframes require an animation name')
        }
        const original = animationName(name)
        const renamed = `${pluginId.replace('.', '-')}--${original}`
        animations.set(original, renamed)
        renameAnimationName(name, renamed)
        rule.params = generate(parsed)
      })
      root.walkRules((rule) => {
        if (inheritsSelectorScope(rule)) return
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
        if (animations.size > 0) renameAnimationReferences(declaration, animations)
      })
    },
  }
}

/** inheritsSelectorScope excludes keyframe steps and rules beneath a scoped style rule.
 * Grouping at-rules alone do not establish selector scope. A style-rule ancestor
 * does, including when a nested media or support query separates the two rules.
 */
function inheritsSelectorScope(rule: Rule): boolean {
  let parent: Rule['parent'] | Document = rule.parent
  while (parent) {
    if (parent.type === 'rule') return true
    if (parent.type === 'atrule' && /^(?:-webkit-)?keyframes$/.test(parent.name)) return true
    parent = parent.parent
  }
  return false
}

/** isAnimationName narrows CSS nodes to literal identifier or string values. */
function isAnimationName(node: CssNode | null): node is AnimationName {
  return node?.type === 'Identifier' || node?.type === 'String'
}

/** animationName decodes the identity shared by keyframe definitions and references. */
function animationName(node: AnimationName): string {
  return node.type === 'Identifier' ? ident.decode(node.name) : node.value
}

/** renameAnimationName replaces a literal while preserving its CSS representation kind.
 * Identifier encoding keeps escaped names valid after prefixing. String values
 * retain string syntax so names containing spaces never become multiple tokens.
 */
function renameAnimationName(node: AnimationName, name: string): void {
  if (node.type === 'Identifier') node.name = ident.encode(name)
  else node.value = name
}

/** renameAnimationReferences associates literal names with their local keyframe definitions.
 * Each comma-separated animation is matched independently against its property
 * grammar. Easing keywords, direction keywords, and function arguments are never
 * renamed merely because their spelling also names a local animation.
 *
 * Substitution functions prevent complete grammar matching. A literal that can
 * only occupy the animation-name position fixes that identity within its item,
 * allowing dynamic durations and easing arguments. Authors must keep those
 * substitutions within non-name components; runtime expansion of whole animation
 * lists cannot be scoped. Without a fixed name, the build reports ambiguity
 * instead of emitting a reference that may miss its renamed local definition.
 */
function renameAnimationReferences(
  declaration: Declaration,
  animations: ReadonlyMap<string, string>,
): void {
  const parsed = parse(declaration.value, { context: 'value', positions: true })
  if (parsed.type !== 'Value') return
  const property = declaration.prop.replace(/^-webkit-/, '')
  const names: AnimationName[] = []
  let group: CssNode[] = []
  for (const node of parsed.children) {
    if (node.type === 'Operator' && node.value === ',') {
      names.push(...animationReferences(group, property, declaration, animations))
      group = []
    } else {
      group.push(node)
    }
  }
  names.push(...animationReferences(group, property, declaration, animations))
  // Replace only identified name spans, retaining author formatting and untouched
  // substitutions after every list item has passed the same identity checks.
  for (const node of names.reverse()) {
    const replacement = animations.get(animationName(node))
    if (!replacement || !node.loc) continue
    renameAnimationName(node, replacement)
    declaration.value =
      declaration.value.slice(0, node.loc.start.offset) +
      generate(node) +
      declaration.value.slice(node.loc.end.offset)
  }
}

/** animationReferences resolves one animation's literal name through the CSS grammar.
 * Dynamic shorthand values require a literal name with no competing shorthand
 * role. Unrelated literal animations pass through even when another list item
 * refers to a local keyframe. Dynamic names fail only while local definitions
 * are being renamed, since their eventual identity cannot be established here.
 *
 * A grammar failure without substitutions preserves values that do not mention
 * local names. Values containing local names fail explicitly rather than leaving
 * a reference behind after the corresponding definition acquires its prefix.
 */
function animationReferences(
  nodes: CssNode[],
  property: string,
  declaration: Declaration,
  animations: ReadonlyMap<string, string>,
): AnimationName[] {
  const value: Value = { type: 'Value', children: new List<CssNode>().fromArray(nodes) }
  const matched = lexer.matchProperty(property, value)
  if (!matched.error) {
    const names: AnimationName[] = []
    walk(value, (node) => {
      if (isAnimationName(node) && matched.isType(node, 'keyframes-name')) names.push(node)
    })
    return names
  }
  const dynamic = find(
    value,
    (node) => node.type === 'Function' && /^(?:var|env|attr)$/i.test(node.name),
  )
  if (dynamic && property === 'animation') {
    const names = nodes.filter(isAnimationName).filter((node) => {
      const standalone = lexer.matchType('single-animation', node)
      return !standalone.error && standalone.isType(node, 'keyframes-name')
    })
    if (names.length === 1) return names
  }
  if (
    dynamic ||
    nodes.some((node) => isAnimationName(node) && animations.has(animationName(node)))
  ) {
    throw declaration.error(
      'Cannot scope this animation name; use a literal animation-name and separate dynamic animation properties',
    )
  }
  return []
}
