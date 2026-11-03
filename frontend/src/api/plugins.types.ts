// plugins.types.ts describes authenticated contribution discovery and shared
// manifest fields. Bundle URLs and integrity values come from the host's accepted
// installation, while configuration schemas retain their publisher-defined order
// and localized labels without exposing stored secret values.

/** LocalizedPluginText maps language tags to publisher-provided display text. */
export type LocalizedPluginText = Record<string, string>

/** PluginFieldType lists the seven configuration controls supported by the host. */
export type PluginFieldType =
  'string' | 'textarea' | 'number' | 'integer' | 'boolean' | 'select' | 'secret'

/** PluginField preserves one validated manifest configuration declaration. */
export interface PluginField {
  key: string // Unique configuration field key.
  type: PluginFieldType // Declared configuration value type.
  label: LocalizedPluginText // Localized configuration field label.
  description?: LocalizedPluginText // Optional localized field explanation.
  required?: boolean // Nonempty value requirement flag.
  default?: string | number | boolean | null // Optional declared ordinary default.
  min?: number // Inclusive numeric lower bound.
  max?: number // Inclusive numeric upper bound.
  maxLength?: number // Unicode character limit for text.
  pattern?: string // Server-authoritative RE2 string pattern.
  options?: PluginFieldOption[] // Declared stored choices and labels.
}

/** PluginManifest carries publisher metadata and supported contribution declarations. */
export interface PluginManifest {
  schemaVersion: number // Manifest schema major version.
  id: string // Publisher-qualified plugin installation identity.
  version: string // Declared release semantic version.
  apiVersion: number // Required host protocol major.
  minHostVersion: string // Minimum compatible host version.
  maxHostVersion?: string // Exclusive compatible host version ceiling.
  name: LocalizedPluginText // Localized plugin display name.
  description: LocalizedPluginText // Localized plugin purpose description.
  publisher: PluginPublisher // Publisher identity and optional homepage.
  license: string // Declared software license identifier.
  icon?: string // Declared public SVG or PNG.
  homepage?: string // Optional publisher product homepage.
  permissions?: string[] // Requested host capability permissions.
  network?: string[] // Declared outbound hosts without enforcement.
  resources?: PluginResources // Advisory native memory allowance.
  config?: PluginConfiguration // Ordered editable configuration schema.
  backend?: PluginBackend // Optional native backend contribution.
  frontend?: PluginFrontend // Optional authenticated frontend contribution.
}

/** PluginAudience distinguishes user and administrator contribution route families. */
export type PluginAudience = 'user' | 'admin'

/** PluginAsset describes a public versioned bundle with its required browser integrity. */
export interface PluginAsset {
  url: string // Host-generated versioned asset URL.
  integrity: string // Accepted SHA-384 integrity attribute.
}

/** PluginPage identifies one server-authorized page with a localized title. */
export interface PluginPage {
  id: string // Registered page component identifier.
  path: string // Plugin-relative static page path.
  audience: PluginAudience // Authorized contribution audience category.
  title: string // Request-localized page display title.
}

/** PluginMenu describes one authorized page link for the existing sidebar renderer. */
export interface PluginMenu {
  page: string // Target page component identifier.
  audience: PluginAudience // Audience inherited from target page.
  label: string // Request-localized sidebar display text.
  icon_svg: string // SVG passed through existing sanitization.
  order: number // Server-normalized sidebar sorting priority.
  route: string // Absolute host contribution page route.
}

/** PluginUI contains visible frontend contributions for the current authenticated scope. */
export interface PluginUI {
  id: string // Publisher-qualified plugin installation identity.
  version: string // Accepted installed release version.
  name: string // Request-localized plugin display name.
  script: PluginAsset // Required IIFE script descriptor.
  styles: PluginAsset[] // Ordered required stylesheet descriptors.
  pages: PluginPage[] // Audience-filtered page contribution list.
  menus: PluginMenu[] // Audience-filtered sidebar contribution list.
}

/** PluginUIResponse wraps all local contributions visible to the current caller. */
export interface PluginUIResponse {
  plugins: PluginUI[] // Ordered visible frontend contribution list.
}

/** PluginFieldOption pairs a stored selection value with its localized label. */
export interface PluginFieldOption {
  value: string // Stored selection option value.
  label: LocalizedPluginText // Localized selection option label.
}

/** PluginPublisher identifies the author presented during installation review. */
export interface PluginPublisher {
  name: string // Publisher display name text.
  url?: string // Optional publisher profile URL.
}

/** PluginResources contains advisory runtime limits declared by the publisher. */
export interface PluginResources {
  memoryMiB?: number // Advisory memory limit in MiB.
}

/** PluginConfiguration retains the ordered fields accepted by an installed release. */
export interface PluginConfiguration {
  fields?: PluginField[] // Ordered editable configuration declarations.
}

/** PluginBackend lists native HTTP and scheduled contributions from the manifest. */
export interface PluginBackend {
  executable: string // Package-relative native executable path.
  routes?: PluginRouteDeclaration[] // Ordered HTTP authorization declarations.
  tasks?: PluginTaskDeclaration[] // Scheduled native task declarations.
}

/** PluginRouteDeclaration defines one ordered HTTP matching and admission rule. */
export interface PluginRouteDeclaration {
  method: string // Exact method or wildcard.
  path: string // Exact or prefix request path.
  auth: 'public' | 'user' | 'admin' // Required route authentication scope.
  timeoutSeconds?: number // Route execution timeout in seconds.
  maxBodyBytes?: number // Maximum accepted request body bytes.
}

/** PluginTaskDeclaration defines one named scheduled native task and its deadline. */
export interface PluginTaskDeclaration {
  name: string // Plugin-local task identity string.
  schedule: string // Cron or every schedule expression.
  timeoutSeconds?: number // Task execution timeout in seconds.
}

/** PluginFrontend describes public bundles and authenticated contribution navigation. */
export interface PluginFrontend {
  entry: string // Required package-relative IIFE entry.
  styles?: string[] // Ordered package-relative stylesheet paths.
  pages: PluginPageDeclaration[] // Declared authenticated contribution pages.
  menus?: PluginMenuDeclaration[] // Declared sidebar page links.
}

/** PluginPageDeclaration binds a registered component to an audience and path. */
export interface PluginPageDeclaration {
  id: string // Registered page component identifier.
  path: string // Plugin-relative static page path.
  audience: PluginAudience // Authorized page audience category.
  title: LocalizedPluginText // Localized page title text.
}

/** PluginMenuDeclaration selects a declared page and supplies its sidebar presentation. */
export interface PluginMenuDeclaration {
  page: string // Target page component identifier.
  label: LocalizedPluginText // Localized sidebar entry label.
  icon?: string // Optional package-relative SVG path.
  order?: number // Optional sidebar sorting priority.
}
