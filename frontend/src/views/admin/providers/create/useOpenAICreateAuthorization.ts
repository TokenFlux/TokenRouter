import type { Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { loadProtocolCatalog, nativeProtocolOptions } from '@/api/admin/protocolCapabilities'
import type { ProtocolID } from '@/api/protocols.types'
import type { CreateProviderRequest, CodexSessionImportMessage } from '@/api/admin/providers.types'
import {
  useOpenAIOAuth,
  type OpenAIOAuthSession,
  type OpenAITokenInfo,
} from '@/composables/useOpenAIOAuth'

// useOpenAICreateAuthorization.ts owns OpenAI authorization batches and Codex
// import submission for the provider creation dialog. Each pasted callback is
// associated with an unused authorization session by state, or by input order
// when state is absent. Successful exchanges consume their session before
// provider persistence, while failures remain visible alongside partial success.
//
// The caller owns editable policy fields and supplies credential and extra
// builders. This controller reads those builders at the same submission points
// as the dialog, preserves import-specific null and omission behavior, and waits
// for the protocol catalog before session import filters native capabilities.
// Batch operations continue after individual failures and close only after
// complete success; partial completion still refreshes the provider list.

// OpenAICreateDraft contains the common creation fields read at each submission.
export type OpenAICreateDraft = Pick<
  CreateProviderRequest,
  | 'name'
  | 'notes'
  | 'proxy_id'
  | 'concurrency'
  | 'load_factor'
  | 'priority'
  | 'rate_multiplier'
  | 'group_ids'
  | 'expires_at'
>

// OpenAICreateAuthorizationOptions connects form policies to OpenAI submission
// without exposing callback matching, batch progress, or import result handling.
interface OpenAICreateAuthorizationOptions {
  form: OpenAICreateDraft // live common provider fields
  autoPauseOnExpired: Ref<boolean> // expiry scheduling policy
  // upstreamProtocols preserves undefined default selection and an explicit empty
  // selection; session imports filter either selection against native capability.
  upstreamProtocols: Ref<ProtocolID[] | undefined>
  inputMethod: () => string | undefined // active authorization input method
  // buildCredentialExtras returns null when temporary rules fail validation;
  // callers must stop submission while retaining the existing visible failure.
  buildCredentialExtras: () => Record<string, unknown> | null
  // applyCredentialPolicies mutates the existing exchanged credentials, including
  // deletion of obsolete model mappings; false prevents persistence after rule validation.
  applyCredentialPolicies: (credentials: Record<string, unknown>) => boolean // mutable OAuth credential policies
  buildExtra: (base?: Record<string, unknown>) => Record<string, unknown> | undefined // provider policy fields
  withRequestIdHeader: (
    base: Record<string, unknown> | undefined,
  ) => Record<string, unknown> | undefined // request diagnostic override
  validateTemporaryRules: () => boolean // preflight batch validation
  tokenTLSRouterId: () => number | null // token request router
  // createProvider applies catalog-based native protocol selection before its
  // request; rejection remains owned by the batch entry that awaited persistence.
  createProvider: (payload: CreateProviderRequest) => Promise<unknown> // protocol-aware creation boundary
  // created refreshes the parent list after any successful persistence, while
  // close dismisses the draft only after the entire operation reports complete success.
  created: () => void // provider list refresh
  close: () => void // completed dialog dismissal
}

// useOpenAICreateAuthorization binds batch and import handlers to one form.
// Invoke it during setup for localization and notifications; returned handlers
// retain live draft readers so each provider receives the current shared fields.
export function useOpenAICreateAuthorization(options: OpenAICreateAuthorizationOptions) {
  const { t } = useI18n()
  const appStore = useAppStore()
  const openaiOAuth = useOpenAIOAuth()
  // OpenAIAuthCodeEntry retains callback input identity and original diagnostic
  // line numbering.
  interface OpenAIAuthCodeEntry {
    lineNumber: number // original one-based line
    code: string // extracted authorization code
    state: string // extracted callback state
  }

  // extractOpenAIAuthParam extracts a trimmed callback parameter from complete
  // URLs or query fragments. Malformed percent encoding keeps the raw matched
  // value so a usable code is not discarded during parsing.
  const extractOpenAIAuthParam = (value: string, param: 'code' | 'state') => {
    try {
      const parsed = new URL(value)
      return (parsed.searchParams.get(param) || '').trim()
    } catch {
      // Pasted callbacks may contain only a query fragment; extract its parameter
      // without requiring the complete URL accepted by the first parser.
      const match = value.match(new RegExp(`(?:^|[?&])${param}=([^&#\\s]+)`))
      if (!match?.[1]) {
        return ''
      }
      try {
        return decodeURIComponent(match[1].replace(/\+/g, ' ')).trim()
      } catch {
        // Preserve raw parameter text after decoding fails so malformed escaping
        // does not discard the administrator's original authorization input.
        return match[1].trim()
      }
    }
  }

  // parseOpenAIAuthCodeEntries preserves callback order and original line numbers
  // while accepting plain codes. URLs and query fragments contribute only their
  // code and state fields, preventing an incomplete callback URL from being
  // submitted as a code. Blank lines are removed after numbering so diagnostics
  // still identify the pasted source line. State-only entries remain available
  // for the batch handler to report their missing authorization code explicitly.
  const parseOpenAIAuthCodeEntries = (input: string): OpenAIAuthCodeEntry[] => {
    return input
      .split(/\r?\n/)
      .map((line, index) => {
        const trimmed = line.trim()
        const code = extractOpenAIAuthParam(trimmed, 'code')
        const state = extractOpenAIAuthParam(trimmed, 'state')
        const hasOAuthParam = /(?:^|[?&])(?:code|state)=/.test(trimmed)
        const looksLikeURL = /^[a-z][a-z\d+.-]*:\/\//i.test(trimmed)
        const plainCode = !hasOAuthParam && !looksLikeURL ? trimmed : ''
        return {
          lineNumber: index + 1,
          code: code || plainCode,
          state,
        }
      })
      .filter((entry) => entry.code || entry.state)
  }

  // getOpenAIAuthSessions snapshots queued authorization sessions for one batch.
  // When the queue is empty, a current single session becomes the fallback;
  // absence of both sources returns an empty list for validation.
  const getOpenAIAuthSessions = (): OpenAIOAuthSession[] => {
    if (openaiOAuth.authSessions.value.length > 0) {
      return [...openaiOAuth.authSessions.value]
    }
    if (!openaiOAuth.sessionId.value) {
      return []
    }
    return [
      {
        authUrl: openaiOAuth.authUrl.value,
        sessionId: openaiOAuth.sessionId.value,
        state: openaiOAuth.oauthState.value,
      },
    ]
  }

  // findOpenAIAuthSession matches a callback to an unused authorization session.
  // Explicit state requires equality, while a plain code uses the first unused
  // session in input order and returns null when none remains.
  const findOpenAIAuthSession = (
    entry: OpenAIAuthCodeEntry,
    sessions: OpenAIOAuthSession[],
    usedSessionIds: Set<string>,
  ) => {
    if (entry.state) {
      return (
        sessions.find(
          (session) => session.state === entry.state && !usedSessionIds.has(session.sessionId),
        ) || null
      )
    }
    return sessions.find((session) => !usedSessionIds.has(session.sessionId)) || null
  }

  // buildOpenAIOAuthProviderRequest combines exchanged credentials with current
  // form policies. An explicit client ID overrides the token-derived value before
  // policy decoration, and invalid temporary rules return null with a visible
  // OAuth error. Shared creation fields retain their original omission and null
  // behavior, including converting a missing load factor to undefined. Model
  // policies are applied to the mutable credential object so their explicit
  // clearing behavior survives existing credential data.
  const buildOpenAIOAuthProviderRequest = (
    tokenInfo: OpenAITokenInfo,
    providerName: string,
    clientId?: string,
  ): CreateProviderRequest | null => {
    const credentials = openaiOAuth.buildCredentials(tokenInfo)
    if (clientId) {
      credentials.client_id = clientId
    }
    const oauthExtra = openaiOAuth.buildExtraInfo(tokenInfo) as Record<string, unknown> | undefined
    const extra = options.withRequestIdHeader(options.buildExtra(oauthExtra))

    if (!options.applyCredentialPolicies(credentials)) {
      openaiOAuth.error.value = t('admin.providers.tempUnschedulable.rulesInvalid')
      return null
    }

    return {
      name: providerName,
      notes: options.form.notes,
      platform: 'openai',
      type: 'oauth',
      credentials,
      extra,
      proxy_id: options.form.proxy_id,
      concurrency: options.form.concurrency,
      load_factor: options.form.load_factor ?? undefined,
      priority: options.form.priority,
      rate_multiplier: options.form.rate_multiplier,
      group_ids: options.form.group_ids,
      expires_at: options.form.expires_at,
      auto_pause_on_expired: options.autoPauseOnExpired.value,
    }
  }

  // createOpenAIOAuthProviderFromToken persists one exchanged token through the
  // protocol-aware boundary. Local policy validation returns false without a
  // request; transport failures propagate so the batch can attach the current
  // input index.
  const createOpenAIOAuthProviderFromToken = async (
    tokenInfo: OpenAITokenInfo,
    providerName: string,
    clientId?: string,
  ) => {
    const payload = buildOpenAIOAuthProviderRequest(tokenInfo, providerName, clientId)
    if (!payload) {
      return false
    }
    await options.createProvider(payload)
    return true
  }

  // buildOpenAIOAuthProviderName selects the form name, token email, or default
  // label. Batches append the original one-based entry position so failed earlier
  // entries do not renumber later successfully created providers.
  const buildOpenAIOAuthProviderName = (
    tokenInfo: OpenAITokenInfo,
    index: number,
    total: number,
  ) => {
    const baseName = options.form.name || tokenInfo.email || 'OpenAI OAuth Provider'
    return total > 1 ? `${baseName} #${index + 1}` : baseName
  }

  // handleOpenAIExchange processes pasted callbacks sequentially and reports
  // aggregate creation results. Each entry consumes at most one unused session,
  // with explicit state taking precedence over queue order. Successful token
  // exchange removes that session before provider creation, so a persistence
  // failure does not leave an already-consumed authorization code available.
  // Individual failures remain indexed by original input line; partial success
  // refreshes the list while keeping the dialog and diagnostics visible.
  const handleOpenAIExchange = async (authCodeInput: string) => {
    const oauthClient = openaiOAuth
    if (!authCodeInput.trim()) return

    const entries = parseOpenAIAuthCodeEntries(authCodeInput)
    if (entries.length === 0) {
      oauthClient.error.value = t('admin.providers.oauth.openai.pleaseEnterAuthCode')
      return
    }

    const sessions = getOpenAIAuthSessions()
    if (sessions.length === 0) {
      oauthClient.error.value = t('admin.providers.oauth.openai.pleaseGenerateAuthUrl')
      appStore.showError(oauthClient.error.value)
      return
    }
    if (!options.validateTemporaryRules()) {
      return
    }

    oauthClient.loading.value = true
    oauthClient.error.value = ''

    let successCount = 0
    let failedCount = 0
    const errors: string[] = []
    const usedSessionIds = new Set<string>()

    try {
      for (let i = 0; i < entries.length; i++) {
        const entry = entries[i]
        const session = findOpenAIAuthSession(entry, sessions, usedSessionIds)
        if (!entry.code) {
          failedCount++
          errors.push(
            `#${entry.lineNumber}: ${t('admin.providers.oauth.openai.pleaseEnterAuthCode')}`,
          )
          continue
        }
        if (!session) {
          failedCount++
          errors.push(
            `#${entry.lineNumber}: ${t('admin.providers.oauth.openai.noMatchingAuthUrl')}`,
          )
          continue
        }

        // Reserve the matching session before state validation so later lines
        // cannot reuse a session already assigned to this callback entry.
        usedSessionIds.add(session.sessionId)
        const stateToUse = entry.state || session.state
        if (!stateToUse) {
          failedCount++
          errors.push(`#${entry.lineNumber}: ${t('admin.providers.oauth.openai.missingState')}`)
          continue
        }

        try {
          const tokenInfo = await oauthClient.exchangeAuthCode(
            entry.code,
            session.sessionId,
            stateToUse,
            options.form.proxy_id,
            options.tokenTLSRouterId(),
          )
          oauthClient.loading.value = true
          if (!tokenInfo) {
            failedCount++
            errors.push(
              `#${entry.lineNumber}: ${oauthClient.error.value || t('admin.providers.oauth.openai.failedToExchangeCode')}`,
            )
            oauthClient.error.value = ''
            continue
          }

          // Exchange consumes the authorization code even when provider creation
          // later fails, so remove its session before attempting persistence.
          oauthClient.removeAuthSession(session.sessionId)
          const providerName = buildOpenAIOAuthProviderName(tokenInfo, i, entries.length)
          const created = await createOpenAIOAuthProviderFromToken(tokenInfo, providerName)
          if (!created) {
            failedCount++
            errors.push(
              `#${entry.lineNumber}: ${oauthClient.error.value || t('admin.providers.failedToCreate')}`,
            )
            oauthClient.error.value = ''
            continue
          }

          successCount++
        } catch (error: any) {
          failedCount++
          const errMsg =
            error.response?.data?.detail || error.message || t('admin.providers.oauth.authFailed')
          errors.push(`#${entry.lineNumber}: ${errMsg}`)
        }
      }

      if (successCount > 0 && failedCount === 0) {
        appStore.showSuccess(
          entries.length > 1
            ? t('admin.providers.oauth.batchSuccess', { count: successCount })
            : t('admin.providers.providerCreated'),
        )
        options.created()
        options.close()
      } else if (successCount > 0 && failedCount > 0) {
        appStore.showWarning(
          t('admin.providers.oauth.batchPartialSuccess', {
            success: successCount,
            failed: failedCount,
          }),
        )
        oauthClient.error.value = errors.join('\n')
        options.created()
      } else {
        oauthClient.error.value = errors.join('\n')
        appStore.showError(t('admin.providers.oauth.batchFailed'))
      }
    } finally {
      oauthClient.loading.value = false
    }
  }

  // Mobile refresh tokens use this client identity for both token validation and
  // persisted credentials, while the default refresh-token path omits an override.
  const OPENAI_MOBILE_RT_CLIENT_ID = 'app_LlGpXReQgckcGGUo2JrYvtJK'
  // formatCodexImportMessages renders indexed server diagnostics with optional
  // provider names. Missing messages produce an empty string, allowing callers to
  // combine errors and warnings without introducing blank diagnostic sections.
  const formatCodexImportMessages = (messages?: CodexSessionImportMessage[]) => {
    return (messages || [])
      .map((item) => {
        const name = item.name ? ` ${item.name}` : ''
        return `#${item.index}${name}: ${item.message}`
      })
      .join('\n')
  }

  // isAgentIdentityImportContent recognizes Agent Identity objects, nonempty
  // arrays, or newline-delimited JSON. Both snake_case and camelCase field names
  // are accepted, while every member must qualify; invalid JSON and empty input
  // return false.
  const isAgentIdentityImportContent = (content: string) => {
    // isAgentIdentityValue checks one parsed value for Agent Identity markers.
    // Nonempty arrays require every member to qualify, while objects accept
    // either the explicit auth mode or an object-valued identity field.
    const isAgentIdentityValue = (value: unknown): boolean => {
      if (Array.isArray(value)) return value.length > 0 && value.every(isAgentIdentityValue)
      if (!value || typeof value !== 'object') return false
      const record = value as Record<string, unknown>
      const authMode = record.auth_mode ?? record.authMode
      const agentIdentity = record.agent_identity ?? record.agentIdentity
      return (
        (typeof authMode === 'string' && authMode.toLowerCase() === 'agentidentity') ||
        (!!agentIdentity && typeof agentIdentity === 'object')
      )
    }

    try {
      return isAgentIdentityValue(JSON.parse(content))
    } catch {
      // Session import also accepts newline-delimited objects; retry each complete
      // line only after parsing the entire input as one JSON value fails.
      const lines = content
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean)
      if (lines.length === 0) return false
      try {
        return lines.every((line) => isAgentIdentityValue(JSON.parse(line)))
      } catch {
        return false
      }
    }
  }

  // handleOpenAIImportCodexSession imports session or Agent Identity input and
  // reports server counts. It waits for protocol metadata before filtering
  // selected native protocols, preserving an explicitly empty selection rather
  // than replacing it with defaults. Created and updated entries both count as
  // success; partial failures retain diagnostics and refresh the list, while
  // complete success closes the dialog. Warning-only or skipped-only outcomes
  // remain visible without publishing a creation event.
  const handleOpenAIImportCodexSession = async (content: string) => {
    const oauthClient = openaiOAuth
    const trimmed = content.trim()
    if (!trimmed) {
      oauthClient.error.value = t('admin.providers.oauth.openai.codexSessionEmpty')
      return
    }
    if (options.inputMethod() === 'agent_identity' && !isAgentIdentityImportContent(trimmed)) {
      oauthClient.error.value = t('admin.providers.oauth.openai.agentIdentityInvalid')
      return
    }

    oauthClient.loading.value = true
    oauthClient.error.value = ''

    try {
      await loadProtocolCatalog()
      // Read policy drafts after catalog loading so native protocol filtering uses
      // the loaded capability list and the current explicit administrator selection.
      const credentialExtras = options.buildCredentialExtras()
      if (credentialExtras === null) {
        return
      }
      const extra = options.buildExtra()
      const result = await adminAPI.providers.importCodexSession({
        content: trimmed,
        name: options.form.name,
        notes: options.form.notes || null,
        proxy_id: options.form.proxy_id,
        concurrency: options.form.concurrency,
        load_factor: options.form.load_factor ?? undefined,
        priority: options.form.priority,
        rate_multiplier: options.form.rate_multiplier,
        group_ids: options.form.group_ids,
        expires_at: options.form.expires_at,
        auto_pause_on_expired: options.autoPauseOnExpired.value,
        credential_extras: {
          ...credentialExtras,
          upstream_protocols: (
            options.upstreamProtocols.value ??
            nativeProtocolOptions('openai', 'oauth', 'personalAccessToken')
          ).filter((id) =>
            nativeProtocolOptions('openai', 'oauth', 'personalAccessToken').includes(id),
          ),
        },
        extra: options.withRequestIdHeader(extra),
        update_existing: true,
      })

      const successCount = result.created + result.updated
      const params = {
        created: result.created,
        updated: result.updated,
        skipped: result.skipped,
        failed: result.failed,
      }

      if (successCount > 0 && result.failed === 0) {
        appStore.showSuccess(t('admin.providers.oauth.openai.codexSessionImportSuccess', params))
        options.created()
        options.close()
        return
      }

      const errorText = formatCodexImportMessages(result.errors)
      const warningText = formatCodexImportMessages(result.warnings)
      oauthClient.error.value = [errorText, warningText].filter(Boolean).join('\n')

      if (result.failed === 0) {
        appStore.showWarning(t('admin.providers.oauth.openai.codexSessionImportSuccess', params))
        return
      }

      if (successCount > 0) {
        appStore.showWarning(t('admin.providers.oauth.openai.codexSessionImportPartial', params))
        options.created()
        return
      }

      appStore.showError(t('admin.providers.oauth.openai.codexSessionImportFailed'))
    } catch (error: any) {
      oauthClient.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        t('admin.providers.oauth.openai.codexSessionImportFailed')
      appStore.showError(oauthClient.error.value)
    } finally {
      oauthClient.loading.value = false
    }
  }

  // handleOpenAIImportCodexPAT submits a trimmed personal access token with
  // current policies. Empty tokens and invalid temporary rules stop locally;
  // successful persistence refreshes the list and closes the dialog, while
  // failures retain the reported error.
  const handleOpenAIImportCodexPAT = async (accessToken: string) => {
    const oauthClient = openaiOAuth
    const trimmed = accessToken.trim()
    if (!trimmed) {
      oauthClient.error.value = t('admin.providers.oauth.openai.codexPatEmpty')
      return
    }

    oauthClient.loading.value = true
    oauthClient.error.value = ''

    try {
      // PAT import passes the current model and temporary-rule policies through its
      // own endpoint, preserving omission when the credential supplement is empty.
      const credentialExtras = options.buildCredentialExtras()
      if (credentialExtras === null) {
        return
      }
      const extra = options.buildExtra()
      await adminAPI.providers.createOpenAICodexPAT({
        access_token: trimmed,
        name: options.form.name,
        notes: options.form.notes || null,
        proxy_id: options.form.proxy_id,
        concurrency: options.form.concurrency,
        load_factor: options.form.load_factor ?? undefined,
        priority: options.form.priority,
        rate_multiplier: options.form.rate_multiplier,
        group_ids: options.form.group_ids,
        expires_at: options.form.expires_at,
        auto_pause_on_expired: options.autoPauseOnExpired.value,
        credential_extras: Object.keys(credentialExtras).length > 0 ? credentialExtras : undefined,
        extra: options.withRequestIdHeader(extra),
      })
      appStore.showSuccess(t('admin.providers.providerCreated'))
      options.created()
      options.close()
    } catch (error: any) {
      oauthClient.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        t('admin.providers.oauth.openai.codexPatImportFailed')
      appStore.showError(oauthClient.error.value)
    } finally {
      oauthClient.loading.value = false
    }
  }

  // handleOpenAIBatchRT validates newline-separated refresh tokens and creates
  // providers sequentially. An optional client ID is sent to token validation and
  // persisted in resulting credentials, while TLS routing is read for each token
  // request. Each failed validation or creation contributes one indexed
  // diagnostic without stopping later entries. Complete success closes the
  // dialog, partial success publishes a list refresh and retains failures, and
  // finally releases the shared OAuth loading indicator.
  const handleOpenAIBatchRT = async (refreshTokenInput: string, clientId?: string) => {
    const oauthClient = openaiOAuth
    if (!refreshTokenInput.trim()) return

    const refreshTokens = refreshTokenInput
      .split('\n')
      .map((rt) => rt.trim())
      .filter((rt) => rt)

    if (refreshTokens.length === 0) {
      oauthClient.error.value = t('admin.providers.oauth.openai.pleaseEnterRefreshToken')
      return
    }
    if (!options.validateTemporaryRules()) {
      return
    }

    oauthClient.loading.value = true
    oauthClient.error.value = ''

    let successCount = 0
    let failedCount = 0
    const errors: string[] = []

    try {
      for (let i = 0; i < refreshTokens.length; i++) {
        try {
          const tokenInfo = await oauthClient.validateRefreshToken(
            refreshTokens[i],
            options.form.proxy_id,
            clientId,
            options.tokenTLSRouterId(),
          )
          oauthClient.loading.value = true
          if (!tokenInfo) {
            failedCount++
            errors.push(`#${i + 1}: ${oauthClient.error.value || 'Validation failed'}`)
            oauthClient.error.value = ''
            continue
          }

          const providerName = buildOpenAIOAuthProviderName(tokenInfo, i, refreshTokens.length)
          const created = await createOpenAIOAuthProviderFromToken(
            tokenInfo,
            providerName,
            clientId,
          )
          if (!created) {
            failedCount++
            errors.push(
              `#${i + 1}: ${oauthClient.error.value || t('admin.providers.failedToCreate')}`,
            )
            oauthClient.error.value = ''
            continue
          }

          successCount++
        } catch (error: any) {
          failedCount++
          const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
          errors.push(`#${i + 1}: ${errMsg}`)
        }
      }

      // Show results
      if (successCount > 0 && failedCount === 0) {
        appStore.showSuccess(
          refreshTokens.length > 1
            ? t('admin.providers.oauth.batchSuccess', { count: successCount })
            : t('admin.providers.providerCreated'),
        )
        options.created()
        options.close()
      } else if (successCount > 0 && failedCount > 0) {
        appStore.showWarning(
          t('admin.providers.oauth.batchPartialSuccess', {
            success: successCount,
            failed: failedCount,
          }),
        )
        oauthClient.error.value = errors.join('\n')
        options.created()
      } else {
        oauthClient.error.value = errors.join('\n')
        appStore.showError(t('admin.providers.oauth.batchFailed'))
      }
    } finally {
      oauthClient.loading.value = false
    }
  }

  // handleOpenAIValidateRT submits refresh tokens using the default Codex
  // authorization client.
  const handleOpenAIValidateRT = (rt: string) => handleOpenAIBatchRT(rt)

  // handleOpenAIValidateMobileRT submits refresh tokens using the explicit mobile
  // authorization client.
  const handleOpenAIValidateMobileRT = (rt: string) =>
    handleOpenAIBatchRT(rt, OPENAI_MOBILE_RT_CLIENT_ID)

  return {
    openaiOAuth,
    handleOpenAIExchange,
    handleOpenAIImportCodexSession,
    handleOpenAIImportCodexPAT,
    handleOpenAIValidateRT,
    handleOpenAIValidateMobileRT,
  }
}
