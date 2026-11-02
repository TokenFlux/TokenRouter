import type { AllowedComponentProps, Component, ComponentCustomProps, Ref, VNodeProps } from 'vue'

// types.ts defines the stable interface shared by plugin pages and their host.
// Requests remain scoped to one plugin, while account, language, theme, and
// navigation values are supplied as readonly references. Runtime implementations
// belong to the host or the explicit local-development mock.

export { normalizePluginPage, type PluginPageRenderer } from './page-component.js'

/** HttpMethod lists the nonstreaming HTTP methods accepted by plugin requests. */
export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

/** HostUser exposes only the verified account fields needed for page presentation. */
export interface HostUser {
  readonly id: number // Verified account identifier.
  readonly username: string // Account display username.
  readonly role: 'user' | 'admin' // Verified account role.
}

/** HostRoute describes the currently mounted page within one plugin contribution. */
export interface HostRoute {
  pageId: string // Manifest page identifier.
  path: string // Plugin-relative page path.
  query: Record<string, string> // Decoded page query values.
}

/** RequestOptions supplies query values, a JSON body, and optional cancellation. */
export interface RequestOptions {
  query?: Record<string, string> // Encoded request query values.
  body?: unknown // JSON request body.
  signal?: AbortSignal // Caller-owned cancellation signal.
}

/** ConfirmOptions describes a host-owned confirmation dialog for a plugin action. */
export interface ConfirmOptions {
  title: string // Confirmation dialog title.
  message: string // Confirmation action explanation.
  danger?: boolean // Destructive action presentation flag.
}

/** HostApi scopes navigation and authenticated requests to the mounted plugin. */
export interface HostApi {
  readonly pluginId: string // Publisher-qualified plugin identity.
  readonly version: string // Loaded plugin release version.
  readonly apiVersion: 1 // Host frontend protocol major.
  readonly user: Readonly<Ref<HostUser | null>> // Verified current account identity.
  readonly locale: Readonly<Ref<string>> // Current host language tag.
  readonly theme: Readonly<Ref<'light' | 'dark'>> // Current host theme selection.
  readonly site: Readonly<{
    name: string // Localized site display name.
    balanceUnitName: string // Site balance unit name.
    balanceUnitSymbol: string // Site balance display symbol.
    timezone: string // Host IANA timezone name.
  }>
  readonly route: Readonly<Ref<HostRoute>> // Current plugin page location.
  /** request sends an authenticated, plugin-relative request through the host client.
   * Paths begin with a slash and remain under this plugin's API prefix.
   * Cancellation belongs to the caller; failures become HostRequestError values.
   */
  request<T = unknown>(method: HttpMethod, path: string, options?: RequestOptions): Promise<T>
  /** navigate changes to a page within this plugin's current audience. */
  navigate(path: string, query?: Record<string, string>): void
  /** refreshAccount reloads the current account and its displayed wallet balance. */
  refreshAccount(): Promise<void>
  readonly toast: {
    /** success displays a host-styled success notification for the supplied message. */
    success(message: string): void
    /** error displays a host-styled failure notification for the supplied message. */
    error(message: string): void
    /** info displays a host-styled informational notification for the supplied message. */
    info(message: string): void
  }
  /** confirm opens a host confirmation and resolves whether the user accepted. */
  confirm(options: ConfirmOptions): Promise<boolean>
}

/** HostRequestError preserves the HTTP status and stable host failure code. */
export class HostRequestError extends Error {
  readonly status: number // Failed HTTP response status.
  readonly code: string // Stable host error code.

  /** constructor creates an error from a failed plugin API request. */
  constructor(message: string, status: number, code: string) {
    super(message)
    this.name = 'HostRequestError'
    this.status = status
    this.code = code
  }
}

/** PluginDefinition maps manifest page identifiers to components or asynchronous loaders. */
export interface PluginDefinition {
  pages: Record<string, Component | (() => Promise<Component>)> // Registered page component implementations.
}

/** Messages maps locale tags to plugin-owned translation dictionaries with English fallback. */
export type Messages = Record<string, Record<string, string>>

/** Translator resolves one plugin message with optional named interpolation values. */
export type Translator = (key: string, params?: Record<string, string | number>) => string

/** ButtonProps exposes host button variants without exposing internal CSS implementation. */
export interface ButtonProps {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger' // Host button visual variant.
  size?: 'sm' | 'md' // Host button size tier.
  loading?: boolean // Pending action loading state.
  disabled?: boolean // Action availability disabled flag.
  onClick?: (event: MouseEvent) => void // Activated button event callback.
}

/** InputProps describes the stable text input and textarea binding contract. */
export interface InputProps {
  modelValue?: string // Current input text value.
  placeholder?: string // Empty input placeholder text.
  disabled?: boolean // Input editing disabled flag.
  maxlength?: number // Maximum accepted text length.
  'onUpdate:modelValue'?: (value: string) => void // Updated text binding callback.
}

/** SelectOption pairs a stored value with its displayed selection label. */
export interface SelectOption {
  value: string | number // Stored selection option value.
  label: string // Displayed selection option label.
}

/** SelectProps defines a single-value selection using host-owned dropdown behavior. */
export interface SelectProps {
  modelValue?: string | number // Current selected option value.
  options: SelectOption[] // Available selection option records.
  disabled?: boolean // Selection editing disabled flag.
  'onUpdate:modelValue'?: (value: string | number) => void // Updated selection binding callback.
}

/** ToggleProps exposes a boolean setting through the host's accessible switch. */
export interface ToggleProps {
  modelValue?: boolean // Current boolean setting value.
  disabled?: boolean // Toggle interaction disabled flag.
  'onUpdate:modelValue'?: (value: boolean) => void // Updated boolean binding callback.
}

/** DialogProps selects host dialog presentation while retaining host focus handling. */
export interface DialogProps {
  open: boolean // Dialog visibility binding value.
  title: string // Dialog accessible title text.
  width?: 'sm' | 'md' | 'lg' // Dialog width size tier.
  'onUpdate:open'?: (value: boolean) => void // Updated dialog visibility callback.
}

/** TableColumn identifies a data field and its localized column title. */
export interface TableColumn {
  key: string // Row property lookup key.
  title: string // Displayed column header text.
  width?: string // Optional CSS column width.
}

/** TableProps supplies rows and columns for the host table wrapper. */
export interface TableProps {
  columns: TableColumn[] // Ordered visible table columns.
  rows: Record<string, unknown>[] // Displayed table row records.
  loading?: boolean // Pending table loading state.
}

/** PaginationProps exposes page navigation independently of the host's internal component. */
export interface PaginationProps {
  page: number // One-based current page number.
  pageSize: number // Maximum records per page.
  total: number // Total matching record count.
  'onUpdate:page'?: (value: number) => void // Updated page selection callback.
}

/** EmptyProps gives empty states a title and optional supporting description. */
export interface EmptyProps {
  title: string // Empty state title text.
  description?: string // Optional empty state explanation.
}

/** BadgeTone lists semantic host badge variants without defining their colors. */
export type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'brand'

/** IconName lists the common host icon names available to plugin pages. */
export type IconName = 'check' | 'calendar' | 'gift' | 'refresh' | 'cog' | 'x' | 'info' | 'user'

/** PluginComponent carries a stable public props shape for Vue template checking. */
export type PluginComponent<Props extends object = Record<never, never>> = new () => {
  // Public props permit Vue presentation attributes while keeping native component
  // internals and implementation-specific event signatures outside the SDK contract.
  $props: Props & VNodeProps & AllowedComponentProps & ComponentCustomProps
}

// CSS_VARIABLES names the existing host theme values exposed as the stable plugin
// styling contract. Plugins use these names instead of copying a second palette.
// Brand remains the primary button fill; accent follows the host theme's native
// link and selection foreground, which uses a brighter primary shade in dark mode.
export const CSS_VARIABLES = [
  '--tr-color-brand',
  '--tr-color-brand-contrast',
  '--tr-color-accent',
  '--tr-color-surface',
  '--tr-color-surface-muted',
  '--tr-color-border',
  '--tr-color-text',
  '--tr-color-text-muted',
  '--tr-color-success',
  '--tr-color-warning',
  '--tr-color-danger',
] as const
