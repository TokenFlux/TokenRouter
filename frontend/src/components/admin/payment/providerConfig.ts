import type { LocalizedUpdate } from '@/i18n/content'

// providerConfig.ts defines the administrator's provider fields, method choices, callback
// paths, and EasyPay configuration conversions. Field metadata tells the dialog which
// values are sensitive, optional, clearable, or initialized with defaults. Callback paths
// are composed with editable origins, while Stripe and Airwallex webhooks are configured
// through their provider dashboards.
//
// EasyPay methods travel through a JSON string inside the provider configuration. Parsing
// retains identifiers and localization alongside display and upstream method codes, and
// serialization filters incomplete entries. Invalid stored JSON yields an empty editable
// list. Callback extraction first removes a recognized suffix, then attempts URL-origin
// parsing, retaining unparseable input for the administrator to inspect.

// ConfigFieldDef describes one provider credential input and its dialog validation and
// serialization rules.
export interface ConfigFieldDef {
  key: string /* configuration field identifier */
  // label overrides the translated field-key label when nonempty; an empty string asks the
  // dialog to translate the key.
  label: string
  // sensitive marks credentials omitted by the API; editing permits blank input and
  // submission omits that blank value.
  sensitive: boolean
  // optional skips required-field validation, allowing the administrator to save this
  // configuration field with no entered value.
  optional?: boolean
  // clearable retains an empty string in submitted configuration, allowing the backend to
  // remove a previously stored value.
  clearable?: boolean
  // defaultValue fills a blank draft field when defaults are applied after provider
  // selection or configuration hydration.
  defaultValue?: string
  hintKey?: string /* translated field hint key */
  // options selects a choice control when supplied; its values become the configuration
  // string stored under this field key.
  options?: TypeOption[]
}

// TypeOption pairs a select value with its display label and additional option metadata.
export interface TypeOption {
  value: string /* submitted option code */
  label: string /* visible option label */
  // Additional metadata can travel with select options, letting consumers attach display
  // information alongside the required value and label fields.
  [key: string]: unknown
}

// EasyPayCustomMethod represents local and upstream payment codes with editable localized
// display content.
export interface EasyPayCustomMethod {
  // id links a custom method to its localized content; parsing older records uses their
  // method code when this identifier is absent.
  id?: string /* persistent record identifier */
  // displayNameLocalization passes through parse and serialization with the method,
  // retaining translated names when configuration returns to the editor.
  displayNameLocalization?: LocalizedUpdate<string> /* translated method display content */
  type: string /* local payment method code */
  upstreamType: string /* upstream payment method code */
  displayName: string /* visible payment method name */
}

// CallbackPaths describes fixed provider URL suffixes appended to independently editable
// callback origins.
export interface CallbackPaths {
  // notifyUrl supplies the fixed webhook suffix when this provider accepts callback
  // configuration; omission hides that origin input from the dialog.
  notifyUrl?: string /* fixed webhook path suffix */
  // returnUrl supplies the browser result suffix when supported; providers without this
  // field omit the return-origin input and outgoing URL.
  returnUrl?: string /* fixed browser-return path suffix */
}

// PROVIDER_SUPPORTED_TYPES lists the built-in choices editable for each provider; custom
// EasyPay definitions extend those choices in the dialog.
export const PROVIDER_SUPPORTED_TYPES: Record<string, string[]> = {
  easypay: ['alipay', 'wxpay'],
  alipay: ['alipay'],
  wxpay: ['wxpay'],
  // Stripe Dashboard controls Checkout payment methods, so the editable configuration
  // exposes one provider-level selection that also owns its shared limits.
  stripe: ['stripe'],
  airwallex: ['airwallex'],
  epusdt: ['epusdt'],
}

// PAYMENT_CURRENCY_OPTIONS lists the currency codes exposed by provider currency
// selectors, using each code as its display label.
export const PAYMENT_CURRENCY_OPTIONS: TypeOption[] = [
  { value: 'CNY', label: 'CNY' },
  { value: 'HKD', label: 'HKD' },
  { value: 'USD', label: 'USD' },
  { value: 'EUR', label: 'EUR' },
  { value: 'GBP', label: 'GBP' },
  { value: 'AUD', label: 'AUD' },
  { value: 'CAD', label: 'CAD' },
  { value: 'SGD', label: 'SGD' },
  { value: 'JPY', label: 'JPY' },
  { value: 'KRW', label: 'KRW' },
  { value: 'NZD', label: 'NZD' },
]

// STRIPE_SDK_API_VERSION supplies the webhook guidance version shown beside Stripe
// configuration, matching the integrated backend SDK API version.
export const STRIPE_SDK_API_VERSION = '2026-03-25.dahlia'

// WEBHOOK_PATHS maps provider keys to application callback routes appended to an origin
// when the dialog displays webhook URLs.
export const WEBHOOK_PATHS: Record<string, string> = {
  easypay: '/api/v1/payment/webhook/easypay',
  alipay: '/api/v1/payment/webhook/alipay',
  wxpay: '/api/v1/payment/webhook/wxpay',
  stripe: '/api/v1/payment/webhook/stripe',
  airwallex: '/api/v1/payment/webhook/airwallex',
  epusdt: '/api/v1/payment/webhook/epusdt',
}

// RETURN_PATH identifies the browser result page appended to configured return origins for
// providers with redirect callback configuration.
export const RETURN_PATH = '/payment/result'

// PROVIDER_CALLBACK_PATHS defines the immutable suffixes shown beside editable callback
// origins; external-dashboard webhook providers have no entries here.
export const PROVIDER_CALLBACK_PATHS: Record<string, CallbackPaths> = {
  easypay: { notifyUrl: WEBHOOK_PATHS.easypay, returnUrl: RETURN_PATH },
  alipay: { notifyUrl: WEBHOOK_PATHS.alipay, returnUrl: RETURN_PATH },
  wxpay: { notifyUrl: WEBHOOK_PATHS.wxpay },
  epusdt: { notifyUrl: WEBHOOK_PATHS.epusdt },
}

// PROVIDER_CONFIG_FIELDS drives credential inputs and validation for each provider;
// callback URLs and custom EasyPay method rows are handled separately.
export const PROVIDER_CONFIG_FIELDS: Record<string, ConfigFieldDef[]> = {
  epusdt: [
    {
      key: 'apiBase',
      label: '',
      sensitive: false,
      hintKey: 'admin.settings.payment.field_epusdtApiBaseHint',
    },
    { key: 'pid', label: 'PID', sensitive: false },
    { key: 'secretKey', label: '', sensitive: true },
    {
      key: 'currency',
      label: '',
      sensitive: false,
      defaultValue: 'CNY',
      options: PAYMENT_CURRENCY_OPTIONS.filter(
        (option) => option.value === 'CNY' || option.value === 'USD',
      ),
    },
  ],
  easypay: [
    { key: 'pid', label: 'PID', sensitive: false },
    { key: 'pkey', label: 'PKey', sensitive: true },
    { key: 'apiBase', label: '', sensitive: false },
    { key: 'cidAlipay', label: '', sensitive: false, optional: true },
    { key: 'cidWxpay', label: '', sensitive: false, optional: true },
  ],
  alipay: [
    { key: 'appId', label: 'App ID', sensitive: false },
    { key: 'privateKey', label: '', sensitive: true },
    { key: 'publicKey', label: '', sensitive: true },
  ],
  wxpay: [
    { key: 'appId', label: 'App ID', sensitive: false },
    { key: 'mchId', label: '', sensitive: false },
    { key: 'privateKey', label: '', sensitive: true },
    { key: 'apiV3Key', label: '', sensitive: true },
    { key: 'certSerial', label: '', sensitive: false },
    { key: 'publicKey', label: '', sensitive: true },
    { key: 'publicKeyId', label: '', sensitive: false },
  ],
  stripe: [
    { key: 'secretKey', label: '', sensitive: true },
    { key: 'publishableKey', label: '', sensitive: false },
    { key: 'webhookSecret', label: '', sensitive: true },
    {
      key: 'currency',
      label: '',
      sensitive: false,
      defaultValue: 'CNY',
      hintKey: 'admin.settings.payment.field_paymentCurrencyHint',
      options: PAYMENT_CURRENCY_OPTIONS,
    },
  ],
  airwallex: [
    { key: 'clientId', label: '', sensitive: false },
    { key: 'apiKey', label: '', sensitive: true },
    { key: 'webhookSecret', label: '', sensitive: true },
    {
      key: 'apiBase',
      label: '',
      sensitive: false,
      defaultValue: 'https://api.airwallex.com/api/v1',
      hintKey: 'admin.settings.payment.field_airwallexApiBaseHint',
    },
    { key: 'countryCode', label: '', sensitive: false, defaultValue: 'CN' },
    {
      key: 'currency',
      label: '',
      sensitive: false,
      defaultValue: 'CNY',
      hintKey: 'admin.settings.payment.field_paymentCurrencyHint',
      options: PAYMENT_CURRENCY_OPTIONS,
    },
    {
      key: 'accountId',
      label: '',
      sensitive: false,
      optional: true,
      clearable: true,
      hintKey: 'admin.settings.payment.field_accountIdHint',
    },
  ],
}

// resolveTypeLabel finds a method option by its exact value in allTypes. Missing options
// receive the raw value as their label; provider and redirect arguments are retained for
// callers.
export function resolveTypeLabel(
  typeVal: string,
  _providerKey: string,
  allTypes: TypeOption[],
  _redirectLabel: string,
): TypeOption {
  // Provider-specific method codes can be missing from shared choices; retain the raw
  // identifier as a visible label for those options.
  return allTypes.find((pt) => pt.value === typeVal) || { value: typeVal, label: typeVal }
}

// getAvailableTypes returns display options for the provider's configured built-in payment
// methods. An unknown provider key yields an empty list, and missing labels resolve to the
// raw method codes.
export function getAvailableTypes(
  providerKey: string,
  allTypes: TypeOption[],
  redirectLabel: string,
): TypeOption[] {
  const types = PROVIDER_SUPPORTED_TYPES[providerKey] || []
  return types.map((t) => resolveTypeLabel(t, providerKey, allTypes, redirectLabel))
}

// parseEasyPayCustomMethods reads stored JSON into editable custom-method records. Empty
// input, malformed JSON, and non-array data return an empty list. Each retained record
// requires local and upstream codes after trimming. Missing identifiers use the local type
// as their identity, and localization data passes through with the display text so editing
// and saving can retain translated names. This parser accepts stored strings; stricter
// code validation belongs to the dialog's save path.
export function parseEasyPayCustomMethods(raw: string | undefined): EasyPayCustomMethod[] {
  if (!raw || !raw.trim()) return []
  try {
    const parsed = JSON.parse(raw)
    // Stored custom methods must be a collection; discard other JSON shapes so the dialog
    // receives an iterable editable list.
    if (!Array.isArray(parsed)) return []
    return parsed
      .map((item) => ({
        // Older entries can lack content IDs; use their local method code to retain an
        // identity when the editor hydrates localization.
        id: String(item?.id || item?.type || ''),
        displayNameLocalization: item?.displayNameLocalization,
        type: String(item?.type || '').trim(),
        upstreamType: String(item?.upstreamType || '').trim(),
        displayName: String(item?.displayName || '').trim(),
      }))
      .filter((item) => item.type && item.upstreamType)
  } catch {
    // Malformed stored method JSON leaves the custom-method editor empty so administrators
    // can replace the configuration through the ordinary save path.
    return []
  }
}

// serializeEasyPayCustomMethods encodes complete custom-method records into the provider
// configuration string. Identifiers and localization accompany trimmed codes and display
// names, preserving the information needed when the editor opens again. Records lacking
// either code are removed from the encoded list. An empty result becomes an empty string,
// which the caller writes as customMethods to clear configured methods. Validation of
// reserved names and duplicate codes remains the dialog's responsibility before saving.
export function serializeEasyPayCustomMethods(methods: EasyPayCustomMethod[]): string {
  const clean = methods
    .map((method) => ({
      id: method.id,
      displayNameLocalization: method.displayNameLocalization,
      type: method.type.trim(),
      upstreamType: method.upstreamType.trim(),
      displayName: method.displayName.trim(),
    }))
    .filter((method) => method.type && method.upstreamType)
  return clean.length ? JSON.stringify(clean) : ''
}

// extractBaseUrl derives an editable callback origin from a stored URL. A recognized path
// suffix is removed exactly, otherwise URL parsing returns the origin and invalid URL text
// is returned unchanged.
export function extractBaseUrl(fullUrl: string, path: string): string {
  if (!fullUrl) return ''
  if (fullUrl.endsWith(path)) return fullUrl.slice(0, -path.length)
  try {
    // An unfamiliar callback suffix cannot be removed exactly; use its parsed origin so
    // the editor can append the selected provider path.
    return new URL(fullUrl).origin
  } catch {
    // If URL parsing fails, retain the supplied text for editing so loading configuration
    // keeps the administrator value available for correction.
    return fullUrl
  }
}
