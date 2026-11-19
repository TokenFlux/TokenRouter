import { describe, expect, it } from 'vitest'
import {
  defaultWeChatConnectScopesForMode,
  normalizeWeChatConnectMode,
} from '../settingsFormHelpers'

/**
 * settings.wechatConnect.spec.ts checks WeChat settings compatibility
 * normalization and default OAuth scopes. Inputs include noisy and historical
 * mode spellings, with assertions retaining the open-platform default and the
 * scope selected for each supported web mode.
 */

describe('admin settings wechat connect helpers', () => {
  /**
   * WeChat modes include uppercase, whitespace, compatibility aliases and an
   * unknown value. Normalization returns canonical modes and uses open for the
   * unsupported value.
   */
  it('normalizes legacy or noisy mode values to the backend contract', () => {
    expect(normalizeWeChatConnectMode('OPEN')).toBe('open')
    expect(normalizeWeChatConnectMode(' open_platform ')).toBe('open')
    expect(normalizeWeChatConnectMode('mp')).toBe('mp')
    expect(normalizeWeChatConnectMode('official_account')).toBe('mp')
    expect(normalizeWeChatConnectMode('unknown')).toBe('open')
  })

  /**
   * Open-platform and public-account modes request their default OAuth scopes.
   * Each resolves to the scope expected by its authorization flow when forms
   * populate default settings.
   */
  it('maps each mode to the backend default scopes', () => {
    expect(defaultWeChatConnectScopesForMode('open')).toBe('snsapi_login')
    expect(defaultWeChatConnectScopesForMode('mp')).toBe('snsapi_userinfo')
  })
})
