import { computed, unref, type ComputedRef, type Ref } from 'vue'
import { getActivePinia } from 'pinia'
import { getLocale } from '@/i18n'
import { useAppStore } from '@/stores'

type MaybeRef<T> = T | Ref<T> | ComputedRef<T>

interface BalanceDisplayOverrides {
  unitName?: MaybeRef<string | null | undefined>
  unitSymbol?: MaybeRef<string | null | undefined>
  iconSvg?: MaybeRef<string | null | undefined>
}

interface BalanceFormatOptions {
  fractionDigits?: number
  withSymbol?: boolean
  fallback?: string
  useGrouping?: boolean
}

const USD_UNIT_NAME = 'USD'
const USD_UNIT_SYMBOL = '$'

function normalizeDisplayText(value: string | null | undefined, fallback: string): string {
  const normalized = value?.trim() ?? ''
  return normalized || fallback
}

export function useBalanceDisplay(overrides: BalanceDisplayOverrides = {}) {
  const appStore = getActivePinia() ? useAppStore() : null
  const injectedConfig = typeof window !== 'undefined' ? window.__APP_CONFIG__ : undefined

  const balanceUnitName = computed(() =>
    normalizeDisplayText(
      unref(overrides.unitName) ??
        appStore?.cachedPublicSettings?.balance_unit_name ??
        injectedConfig?.balance_unit_name,
      USD_UNIT_NAME
    )
  )

  const balanceUnitSymbol = computed(() =>
    normalizeDisplayText(
      unref(overrides.unitSymbol) ??
        appStore?.cachedPublicSettings?.balance_unit_symbol ??
        injectedConfig?.balance_unit_symbol,
      USD_UNIT_SYMBOL
    )
  )

  const balanceIconSvg = computed(() => {
    const svg =
      unref(overrides.iconSvg) ??
      appStore?.cachedPublicSettings?.balance_icon_svg ??
      injectedConfig?.balance_icon_svg ??
      ''
    return svg.trim()
  })

  const hasCustomBalanceIcon = computed(() => balanceIconSvg.value.length > 0)

  function formatAmount(
    value: number | null | undefined,
    unitSymbol: string,
    options: BalanceFormatOptions = {}
  ): string {
    if (value == null) {
      return options.fallback ?? `${options.withSymbol === false ? '' : unitSymbol}0.00`
    }

    const amount = Number(value)
    if (!Number.isFinite(amount)) {
      return options.fallback ?? `${options.withSymbol === false ? '' : unitSymbol}0.00`
    }

    const fractionDigits =
      options.fractionDigits ?? (Math.abs(amount) > 0 && Math.abs(amount) < 0.01 ? 6 : 2)
    const formatted = new Intl.NumberFormat(getLocale(), {
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
      useGrouping: options.useGrouping ?? true
    }).format(amount)

    if (options.withSymbol === false) {
      return formatted
    }
    return `${unitSymbol}${formatted}`
  }

  function formatBalanceAmount(
    value: number | null | undefined,
    options: BalanceFormatOptions = {}
  ): string {
    return formatAmount(value, balanceUnitSymbol.value, options)
  }

  function formatUsdAmount(
    value: number | null | undefined,
    options: BalanceFormatOptions = {}
  ): string {
    return formatAmount(value, USD_UNIT_SYMBOL, options)
  }

  return {
    balanceUnitName,
    balanceUnitSymbol,
    usdUnitName: USD_UNIT_NAME,
    usdUnitSymbol: USD_UNIT_SYMBOL,
    balanceIconSvg,
    hasCustomBalanceIcon,
    formatBalanceAmount,
    formatUsdAmount
  }
}
