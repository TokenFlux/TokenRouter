import type {
  BadgeTone,
  ButtonProps,
  DialogProps,
  EmptyProps,
  HostApi,
  IconName,
  InputProps,
  Messages,
  PaginationProps,
  PluginComponent,
  PluginDefinition,
  SelectProps,
  TableProps,
  ToggleProps,
  Translator,
} from './types.js'

// index.ts publishes frontend declarations without bundling a Vue runtime or
// host component implementation. Production builds externalize this module to
// the host-injected SDK global; the Vite preset substitutes the explicit mock
// module only when running a local development server.

export * from './types.js'

/** definePlugin registers page components during synchronous plugin script execution.
 * The host obtains identity from the current script element. Calling this
 * outside script evaluation fails because no installed plugin identity is available.
 */
export declare function definePlugin(definition: PluginDefinition): void

/** useHost retrieves the host interface provided to the mounted plugin page.
 * Call it during component setup or a functional component's synchronous render.
 * Using it outside a mounted plugin page fails rather than returning an unscoped
 * authenticated request client.
 */
export declare function useHost(): HostApi

/** useMessages returns a translator that follows the current host language.
 * Lookup selects the exact tag, base language, then English. Named parameters
 * replace matching braces without interpreting translated text as HTML.
 */
export declare function useMessages(messages: Messages): Translator

// These component declarations fix the public props supported by the host's
// wrappers. Internal component implementations and unrelated props remain private.
export declare const TrButton: PluginComponent<ButtonProps>
export declare const TrCard: PluginComponent
export declare const TrInput: PluginComponent<InputProps>
export declare const TrTextarea: PluginComponent<InputProps>
export declare const TrSelect: PluginComponent<SelectProps>
export declare const TrToggle: PluginComponent<ToggleProps>
export declare const TrDialog: PluginComponent<DialogProps>
export declare const TrTable: PluginComponent<TableProps>
export declare const TrPagination: PluginComponent<PaginationProps>
export declare const TrEmpty: PluginComponent<EmptyProps>
export declare const TrSkeleton: PluginComponent<{
  rows?: number // Visible skeleton row count.
}>
export declare const TrIcon: PluginComponent<{
  name: IconName // Registered common icon name.
}>
export declare const TrBadge: PluginComponent<{
  tone?: BadgeTone // Semantic host badge tone.
}>
export declare const TrCaptcha: PluginComponent<{
  onVerified?: (token: string) => void // Completed captcha token callback.
  onExpired?: () => void // Expired captcha challenge callback.
}>
export declare const TrAmount: PluginComponent<{
  value: string // Internal decimal balance amount.
}>

/** SdkRuntime is the host-provided object used by externalized plugin imports. */
export type SdkRuntime = Omit<typeof import('./index.js'), 'CSS_VARIABLES'> & {
  CSS_VARIABLES: typeof import('./types.js').CSS_VARIABLES // Supported host theme variable names.
}
