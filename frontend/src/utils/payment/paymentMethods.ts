/**
 * paymentMethods.ts defines shared visible method ordering, built-in aliases, launch modes,
 * and popup geometry. User checkout and administrator controls import these browser-facing
 * rules without needing provider credential schemas or editable configuration fields.
 */
// METHOD_ORDER places built-in wallet methods before card gateways while callers retain custom
// methods after known entries.
export const METHOD_ORDER = [
  'alipay',
  'alipay_direct',
  'wxpay',
  'wxpay_direct',
  'stripe',
  'airwallex',
  'epusdt',
] as const

/**
 * isBuiltInAlipayMethod reports whether a method is an exact built-in Alipay alias.
 */
export function isBuiltInAlipayMethod(type: string): boolean {
  return type === 'alipay' || type === 'alipay_direct'
}

/**
 * isBuiltInWxpayMethod reports whether a method is an exact built-in WeChat alias.
 */
export function isBuiltInWxpayMethod(type: string): boolean {
  return type === 'wxpay' || type === 'wxpay_direct'
}

// Payment modes distinguish QR waiting, hosted popup launch, and the official Alipay redirect
// checkout configuration.
export const PAYMENT_MODE_QRCODE = 'qrcode'
export const PAYMENT_MODE_POPUP = 'popup'
export const PAYMENT_MODE_REDIRECT = 'redirect'

// Preferred dimensions leave room for the Alipay QR and account panels before available-screen
// bounds are applied.
const PAYMENT_POPUP_PREFERRED_WIDTH = 1250
const PAYMENT_POPUP_PREFERRED_HEIGHT = 900

/**
 * getPaymentPopupFeatures sizes and centers a checkout popup within available screen
 * dimensions. Environments without window use the preferred dimensions; callers pass the
 * returned string directly to window.open for payment launch.
 */
export function getPaymentPopupFeatures(): string {
  const screen = typeof window !== 'undefined' ? window.screen : null
  const availW = screen?.availWidth ?? PAYMENT_POPUP_PREFERRED_WIDTH
  const availH = screen?.availHeight ?? PAYMENT_POPUP_PREFERRED_HEIGHT
  // Leave space around the popup for window controls when the available desktop is smaller
  // than the preferred checkout size.
  const width = Math.min(PAYMENT_POPUP_PREFERRED_WIDTH, availW - 40)
  const height = Math.min(PAYMENT_POPUP_PREFERRED_HEIGHT, availH - 40)
  const left = Math.max(0, Math.floor((availW - width) / 2))
  const top = Math.max(0, Math.floor((availH - height) / 2))
  return `width=${width},height=${height},left=${left},top=${top},scrollbars=yes,resizable=yes`
}
