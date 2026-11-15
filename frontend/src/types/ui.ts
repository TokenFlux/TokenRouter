// ui.ts defines option records, filter summary chips, and toast messages consumed by
// shared UI controls. Selection values may include null or booleans, while toast duration
// is expressed in milliseconds and may be omitted to keep the notification visible.

// SelectOption supplies the value and display label consumed by selection controls.
export interface SelectOption {
  value: string | number | boolean | null // Submitted selection value.
  label: string // Human-readable display label.
  [key: string]: any // Support extra properties for custom templates
}

// UsageExportFilterLabel pairs a filter key with its human-readable dimension and value.
export interface UsageExportFilterLabel {
  key: string // Stable filter field identifier.
  label: string // Translated filter dimension label.
  value: string // Human-readable selected filter value.
}

// ToastType selects the visual severity used by notification toasts.
export type ToastType = 'success' | 'error' | 'info' | 'warning'

// Toast contains one transient notification displayed by the application shell.
export interface Toast {
  id: string // Record string identifier.
  type: ToastType // Notification visual severity.
  message: string // Human-readable result message.
  title?: string // Optional notification title.
  // Omit the duration to keep this notification visible; a supplied duration schedules
  // dismissal after that many milliseconds.
  duration?: number // Visibility duration milliseconds.
}
