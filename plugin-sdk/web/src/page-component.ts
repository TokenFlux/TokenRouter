import type { Component, FunctionalComponent } from 'vue'

// page-component.ts adapts registered plugin pages for both the production host
// and the development host. The registration contract accepts Vue components and
// legacy asynchronous loaders, which can both be plain functions without identifying
// metadata. The adapter lets Vue invoke those functions during rendering before
// distinguishing a synchronous rendered value from a promised component definition.
// It never invokes a function early to guess its kind.
//
// Functional components retain Vue's props, event, attribute, slot, and injection
// handling. A promised result is reused through one asynchronous component with a
// bounded loading timeout. Callers supply their existing Vue rendering functions,
// preventing the shared SDK helper from importing another Vue runtime when package
// dependencies resolve from a different installation directory.

/** PluginPageRenderer supplies the mounting host's existing Vue rendering functions. */
export type PluginPageRenderer = Pick<typeof import('vue'), 'h' | 'defineAsyncComponent'>

/** normalizePluginPage preserves functional pages and legacy asynchronous page loaders.
 * Call once per selected page identity and reuse the returned component for that
 * mount. Supply the same Vue runtime functions that mount the host application.
 * Object components keep their original identity and do not need an adapter.
 *
 * Function invocation happens only when Vue renders the adapter. Synchronous values
 * retain normal functional rendering and reactive dependency tracking. A promised
 * definition creates one cached async component, retaining Vue's module-default
 * unwrapping and error propagation while preventing rerenders from rerunning the
 * loader. Functional props and event metadata remain owned by Vue.
 */
export function normalizePluginPage(
  page: Component | (() => Promise<Component>),
  renderer: PluginPageRenderer,
): Component {
  if (typeof page !== 'function') return page
  const functional = page as FunctionalComponent
  let asynchronous: Component | null = null

  /** renderPage evaluates a function in Vue's render context and retains any async result.
   * Vue provides props, attributes, slots, and event emission for this functional
   * component. A pending loader is invoked only during its first render.
   */
  const renderPage: FunctionalComponent = (props, context) => {
    if (asynchronous) return renderer.h(asynchronous, props, context.slots)
    const result: unknown = functional(props, context)
    if (
      result !== null &&
      typeof result === 'object' &&
      'then' in result &&
      typeof result.then === 'function'
    ) {
      // Retain this first promise before rendering its async wrapper, so reactive
      // parent updates cannot start another loader while this component is pending.
      asynchronous = renderer.defineAsyncComponent({
        loader: () => Promise.resolve(result as Promise<Component>),
        // Fifteen seconds bounds unresolved pages before host error recovery appears.
        timeout: 15000,
      })
      return renderer.h(asynchronous, props, context.slots)
    }
    return result
  }
  renderPage.props = functional.props
  renderPage.emits = functional.emits
  renderPage.inheritAttrs = functional.inheritAttrs
  renderPage.displayName = functional.displayName
  return renderPage
}
