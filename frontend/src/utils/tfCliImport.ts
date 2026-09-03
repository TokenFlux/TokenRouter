const TF_WEB_IMPORT_PROTOCOL = 1
const TF_PORT_FIRST = 43110
const TF_PORT_LAST = 43119
const TF_DISCOVERY_TIMEOUT_MS = 30_000
const TF_SESSION_MAX_AGE_MS = 10 * 60_000
const TF_SESSION_PROOF_HEADER = 'X-TF-Session-Proof'
const TF_SESSION_FRAGMENT = /^#tf=1\.(4311[0-9])\.([A-Za-z0-9_-]{22})$/

interface LoopbackRequestInit extends RequestInit {
  targetAddressSpace: 'loopback'
}

interface TfCliImportSession {
  port: number
  secret: ArrayBuffer
  expiresAt: number
}

export interface TfCliTarget {
  port: number
  verified: boolean
}

export interface TfCliImportPayload {
  key: string
  host: string
  key_name?: string
  group_id?: number
  group_name?: string
}

export type TfCliImportResult =
  | { ok: true; status: 202 }
  | { ok: false; status?: number; error: string }

interface TfPingResponse {
  service?: string
  protocol?: number
  proof?: string
}

let pendingSession: TfCliImportSession | null = null
let sessionExpiryTimer: number | null = null
const verifiedSessions = new WeakMap<TfCliTarget, TfCliImportSession>()

function encodeBase64Url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function decodeBase64Url(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4)
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index)
  return bytes.buffer
}

function clearSecret(secret: ArrayBuffer): void {
  new Uint8Array(secret).fill(0)
}

function isKeysPath(pathname: string): boolean {
  return pathname.replace(/\/+$/, '').endsWith('/keys')
}

// @project-doc docs/interfaces/tf_cli_web_import.md#session_fragment
export function initializeTfCliImportSession(): boolean {
  if (typeof window === 'undefined') return false

  const fragment = window.location.hash
  if (!fragment.startsWith('#tf=')) return getTfCliImportSession() !== null

  window.history.replaceState(
    window.history.state,
    '',
    window.location.pathname + window.location.search,
  )
  clearTfCliImportSession()

  if (!isKeysPath(window.location.pathname)) return false

  const match = TF_SESSION_FRAGMENT.exec(fragment)
  if (!match) return false

  try {
    const port = Number(match[1])
    const secret = decodeBase64Url(match[2])
    const canonical = encodeBase64Url(new Uint8Array(secret))
    if (secret.byteLength !== 16 || canonical !== match[2]) return false

    pendingSession = { port, secret, expiresAt: Date.now() + TF_SESSION_MAX_AGE_MS }
    sessionExpiryTimer = window.setTimeout(clearTfCliImportSession, TF_SESSION_MAX_AGE_MS)
    return true
  } catch {
    return false
  }
}

function getTfCliImportSession(): TfCliImportSession | null {
  if (!pendingSession) return null
  if (Date.now() >= pendingSession.expiresAt) {
    clearTfCliImportSession()
    return null
  }
  return pendingSession
}

export function clearTfCliImportSession(): void {
  if (sessionExpiryTimer !== null && typeof window !== 'undefined') {
    window.clearTimeout(sessionExpiryTimer)
  }
  sessionExpiryTimer = null
  if (pendingSession) clearSecret(pendingSession.secret)
  pendingSession = null
}

function createChallenge(): string {
  const bytes = new Uint8Array(18)
  crypto.getRandomValues(bytes)
  return encodeBase64Url(bytes)
}

async function signSessionMessage(secret: ArrayBuffer, message: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    'raw',
    secret,
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  )
  const signature = await crypto.subtle.sign(
    'HMAC',
    key,
    new TextEncoder().encode(message),
  )
  return encodeBase64Url(new Uint8Array(signature))
}

function equalProof(left: string, right: string): boolean {
  if (left.length !== right.length) return false
  let difference = 0
  for (let index = 0; index < left.length; index++) {
    difference |= left.charCodeAt(index) ^ right.charCodeAt(index)
  }
  return difference === 0
}

function loopbackRequest(init: RequestInit, signal?: AbortSignal): LoopbackRequestInit {
  return {
    ...init,
    mode: 'cors',
    cache: 'no-store',
    credentials: 'omit',
    redirect: 'error',
    referrerPolicy: 'no-referrer',
    signal,
    targetAddressSpace: 'loopback',
  }
}

async function pingTfCli(
  port: number,
  session: TfCliImportSession | null,
  signal: AbortSignal,
): Promise<TfCliTarget | null> {
  try {
    const challenge = session ? createChallenge() : ''
    const query = challenge ? `?challenge=${encodeURIComponent(challenge)}` : ''
    const response = await fetch(
      `http://127.0.0.1:${port}/ping${query}`,
      loopbackRequest({ method: 'GET' }, signal),
    )
    if (!response.ok) return null

    const data = await response.json() as TfPingResponse
    if (data.service !== 'tf' || data.protocol !== TF_WEB_IMPORT_PROTOCOL) return null

    const target: TfCliTarget = { port, verified: false }
    if (session && challenge && typeof data.proof === 'string') {
      const expected = await signSessionMessage(
        session.secret,
        `tf-web-import-v1\n${port}\n${challenge}`,
      )
      target.verified = equalProof(data.proof, expected)
      if (target.verified) verifiedSessions.set(target, session)
    }
    return target
  } catch {
    return null
  }
}

// @project-doc docs/interfaces/tf_cli_web_import.md#session_proof
export async function findTfCli(
  signal?: AbortSignal,
  timeoutMs = TF_DISCOVERY_TIMEOUT_MS,
): Promise<TfCliTarget | null> {
  if (signal?.aborted) return null

  const controller = new AbortController()
  const abort = () => controller.abort()
  signal?.addEventListener('abort', abort, { once: true })
  const timeout = window.setTimeout(abort, timeoutMs)

  try {
    const session = getTfCliImportSession()
    if (session) {
      const linked = await pingTfCli(session.port, session, controller.signal)
      if (linked || controller.signal.aborted) return linked
    }

    const ports = Array.from(
      { length: TF_PORT_LAST - TF_PORT_FIRST + 1 },
      (_, index) => TF_PORT_FIRST + index,
    )
    const results = await Promise.all(
      ports.map((port) => pingTfCli(port, null, controller.signal)),
    )
    return results.find((target): target is TfCliTarget => target !== null) ?? null
  } finally {
    window.clearTimeout(timeout)
    signal?.removeEventListener('abort', abort)
  }
}

function sessionProofExpired(session: TfCliImportSession | undefined): boolean {
  return !session || session !== getTfCliImportSession()
}

export async function importKeyToTf(
  target: TfCliTarget,
  payload: TfCliImportPayload,
  signal?: AbortSignal,
): Promise<TfCliImportResult> {
  const body = JSON.stringify({ ...payload, version: TF_WEB_IMPORT_PROTOCOL })
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const verifiedSession = verifiedSessions.get(target)
  if (target.verified && sessionProofExpired(verifiedSession)) {
    verifiedSessions.delete(target)
    return { ok: false, error: 'session_expired' }
  }

  try {
    if (verifiedSession) {
      let proof: string
      try {
        proof = await signSessionMessage(
          verifiedSession.secret,
          `tf-web-import-v1\n${target.port}\nimport\n${body}`,
        )
      } catch {
        verifiedSessions.delete(target)
        return { ok: false, error: 'session_proof_unavailable' }
      }
      if (sessionProofExpired(verifiedSession)) {
        verifiedSessions.delete(target)
        return { ok: false, error: 'session_expired' }
      }
      headers[TF_SESSION_PROOF_HEADER] = proof
    }

    const response = await fetch(
      `http://127.0.0.1:${target.port}/import`,
      loopbackRequest({ method: 'POST', headers, body }, signal),
    )
    let data: { ok?: boolean; error?: string } = {}
    try {
      data = await response.json() as { ok?: boolean; error?: string }
    } catch {
      // A non-JSON local response is treated as an unknown service error.
    }

    if (response.status === 202 && data.ok === true) {
      verifiedSessions.delete(target)
      clearTfCliImportSession()
      return { ok: true, status: 202 }
    }

    if (data.error === 'cancelled') clearTfCliImportSession()
    return {
      ok: false,
      status: response.status,
      error: typeof data.error === 'string' ? data.error : 'unexpected_response',
    }
  } catch (error) {
    if (signal?.aborted || (error instanceof DOMException && error.name === 'AbortError')) {
      return { ok: false, error: 'cancelled' }
    }
    return { ok: false, error: 'network_error' }
  }
}
