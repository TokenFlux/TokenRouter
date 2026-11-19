import { describe, expect, it } from 'vitest'
import {
  appendAuthSourceDefaultsToUpdateRequest,
  buildAuthSourceDefaultsState,
} from '../settingsFormHelpers'
import type { UpdateSettingsRequest } from '@/api/admin/settings.types'

/**
 * settings.authSourceDefaults.spec.ts checks conversion between flat settings
 * and per-source editable defaults. Cases distinguish source-specific values,
 * absent signup-grant flags and mutation of an existing update payload that
 * already contains another settings field.
 */

describe('admin settings auth source defaults helpers', () => {
  /**
   * Flat settings contain different email and LinuxDo defaults with missing
   * other sources. Conversion keeps each source's values together and supplies
   * the expected defaults for absent entries.
   */
  it('builds auth source defaults state from flat settings fields', () => {
    const state = buildAuthSourceDefaultsState({
      auth_source_default_email_balance: 9.5,
      auth_source_default_email_concurrency: 3,
      auth_source_default_email_subscriptions: [{ plan_id: 1 }],
      auth_source_default_email_grant_on_signup: false,
      auth_source_default_email_grant_on_first_bind: true,
      auth_source_default_linuxdo_balance: 6,
      auth_source_default_linuxdo_concurrency: 8,
      auth_source_default_linuxdo_subscriptions: [{ plan_id: 2 }],
      auth_source_default_linuxdo_grant_on_signup: true,
      auth_source_default_linuxdo_grant_on_first_bind: false,
    })

    expect(state.email).toEqual({
      balance: 9.5,
      concurrency: 3,
      subscriptions: [{ plan_id: 1 }],
      grant_on_signup: false,
      grant_on_first_bind: true,
    })
    expect(state.linuxdo).toEqual({
      balance: 6,
      concurrency: 8,
      subscriptions: [{ plan_id: 2 }],
      grant_on_signup: true,
      grant_on_first_bind: false,
    })
    expect(state.oidc).toEqual({
      balance: 0,
      concurrency: 5,
      subscriptions: [],
      grant_on_signup: false,
      grant_on_first_bind: false,
    })
    expect(state.wechat).toEqual({
      balance: 0,
      concurrency: 5,
      subscriptions: [],
      grant_on_signup: false,
      grant_on_first_bind: false,
    })
  })

  /**
   * Authentication-source settings are absent when form state is built.
   * Signup-grant flags default false for the asserted sources, preventing
   * missing configuration from enabling automatic grants.
   */
  it('defaults grant-on-signup to disabled when settings are missing', () => {
    const state = buildAuthSourceDefaultsState({})

    expect(state.email.grant_on_signup).toBe(false)
    expect(state.linuxdo.grant_on_signup).toBe(false)
    expect(state.oidc.grant_on_signup).toBe(false)
    expect(state.wechat.grant_on_signup).toBe(false)
  })

  /**
   * A settings update already contains an unrelated site name before source
   * defaults are appended. Mutation retains that field and writes each source's
   * subscription and grant choices separately.
   */
  it('appends auth source defaults back onto update payload', () => {
    const payload: UpdateSettingsRequest = {
      site_name: 'TokenRouter',
    }

    appendAuthSourceDefaultsToUpdateRequest(payload, {
      email: {
        balance: 1.25,
        concurrency: 2,
        subscriptions: [{ plan_id: 3 }],
        grant_on_signup: true,
        grant_on_first_bind: false,
      },
      linuxdo: {
        balance: 0,
        concurrency: 6,
        subscriptions: [],
        grant_on_signup: false,
        grant_on_first_bind: true,
      },
      oidc: {
        balance: 4,
        concurrency: 9,
        subscriptions: [{ plan_id: 9 }],
        grant_on_signup: true,
        grant_on_first_bind: true,
      },
      wechat: {
        balance: 2,
        concurrency: 5,
        subscriptions: [],
        grant_on_signup: false,
        grant_on_first_bind: false,
      },
      github: {
        balance: 0,
        concurrency: 5,
        subscriptions: [],
        grant_on_signup: false,
        grant_on_first_bind: false,
      },
      google: {
        balance: 0,
        concurrency: 5,
        subscriptions: [],
        grant_on_signup: false,
        grant_on_first_bind: false,
      },
      dingtalk: {
        balance: 0,
        concurrency: 5,
        subscriptions: [],
        grant_on_signup: false,
        grant_on_first_bind: false,
      },
    })

    expect(payload).toMatchObject({
      site_name: 'TokenRouter',
      auth_source_default_email_balance: 1.25,
      auth_source_default_email_concurrency: 2,
      auth_source_default_email_subscriptions: [{ plan_id: 3 }],
      auth_source_default_email_grant_on_signup: true,
      auth_source_default_email_grant_on_first_bind: false,
      auth_source_default_linuxdo_balance: 0,
      auth_source_default_linuxdo_concurrency: 6,
      auth_source_default_linuxdo_subscriptions: [],
      auth_source_default_linuxdo_grant_on_signup: false,
      auth_source_default_linuxdo_grant_on_first_bind: true,
      auth_source_default_oidc_balance: 4,
      auth_source_default_oidc_concurrency: 9,
      auth_source_default_oidc_subscriptions: [{ plan_id: 9 }],
      auth_source_default_oidc_grant_on_signup: true,
      auth_source_default_oidc_grant_on_first_bind: true,
      auth_source_default_wechat_balance: 2,
      auth_source_default_wechat_concurrency: 5,
      auth_source_default_wechat_subscriptions: [],
      auth_source_default_wechat_grant_on_signup: false,
      auth_source_default_wechat_grant_on_first_bind: false,
    })
  })
})
