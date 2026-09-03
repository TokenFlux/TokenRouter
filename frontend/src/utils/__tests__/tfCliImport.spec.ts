import { Buffer } from 'node:buffer'
import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  clearTfCliImportSession,
  findTfCli,
  importKeyToTf,
  initializeTfCliImportSession,
} from '../tfCliImport'

const sessionSecret = 'AAECAwQFBgcICQoLDA0ODw'
const deterministicChallenge = 'AAECAwQFBgcICQoLDA0ODxAR'
const deterministicPingProof = 'CTCnT8q258NPygW9q6J1TMhisl3PBhbk7FCQ-YXgfEk'
const deterministicImportProof = 'f5CZvZfgAVJoIiTFPt2SWirCyvzoKJvdK9qTdLRSbWU'

function setURL(path: string): void {
  window.history.replaceState({}, '', path)
}

function toNodeBuffer(source: BufferSource): Buffer {
  const bytes = ArrayBuffer.isView(source)
    ? new Uint8Array(source.buffer, source.byteOffset, source.byteLength)
    : new Uint8Array(source)
  return Buffer.from(Array.from(bytes))
}

function deterministicCrypto(): Crypto {
  const subtle = {
    importKey(
      format: string,
      keyData: BufferSource,
      algorithm: AlgorithmIdentifier,
      extractable: boolean,
      keyUsages: KeyUsage[],
    ) {
      if (format !== 'raw') throw new Error(`Unexpected key format: ${format}`)
      return webcrypto.subtle.importKey(
        'raw',
        toNodeBuffer(keyData),
        algorithm as HmacImportParams,
        extractable,
        keyUsages,
      )
    },
    sign(algorithm: AlgorithmIdentifier, key: CryptoKey, data: BufferSource) {
      return webcrypto.subtle.sign(algorithm, key, toNodeBuffer(data))
    },
  } as unknown as SubtleCrypto

  return {
    subtle,
    getRandomValues<T extends ArrayBufferView | null>(array: T): T {
      if (!array) return array
      const bytes = new Uint8Array(array.buffer, array.byteOffset, array.byteLength)
      bytes.forEach((_, index) => { bytes[index] = index })
      return array
    },
    randomUUID: webcrypto.randomUUID.bind(webcrypto),
  } as Crypto
}

describe('tfCliImport session lifecycle', () => {
  beforeEach(() => {
    clearTfCliImportSession()
    setURL('/keys')
  })

  afterEach(() => {
    clearTfCliImportSession()
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('captures a canonical session fragment and removes it from the URL immediately', () => {
    const storageSpy = vi.spyOn(Storage.prototype, 'setItem')
    setURL(`/keys?tab=active#tf=1.43110.${sessionSecret}`)

    expect(initializeTfCliImportSession()).toBe(true)
    expect(window.location.pathname + window.location.search + window.location.hash).toBe('/keys?tab=active')
    expect(storageSpy).not.toHaveBeenCalled()
  })

  it.each([
    '#tf=2.43110.AAECAwQFBgcICQoLDA0ODw',
    '#tf=1.43120.AAECAwQFBgcICQoLDA0ODw',
    '#tf=1.43110.AAAAAAAAAAAAAAAAAAAAAB',
    '#tf=malformed',
  ])('clears malformed tf fragments without retaining a session: %s', (fragment) => {
    setURL(`/keys${fragment}`)

    expect(initializeTfCliImportSession()).toBe(false)
    expect(window.location.hash).toBe('')
  })

  it('clears a tf fragment outside the Keys route without accepting it', () => {
    setURL(`/login#tf=1.43110.${sessionSecret}`)

    expect(initializeTfCliImportSession()).toBe(false)
    expect(window.location.hash).toBe('')
  })

  it('expires an in-memory session after ten minutes', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-03T00:00:00Z'))
    setURL(`/keys#tf=1.43110.${sessionSecret}`)
    initializeTfCliImportSession()

    vi.setSystemTime(new Date('2026-09-03T00:10:00Z'))

    expect(initializeTfCliImportSession()).toBe(false)
  })
})

describe('tfCliImport localhost protocol', () => {
  beforeEach(() => {
    clearTfCliImportSession()
    setURL('/keys')
    vi.stubGlobal('crypto', deterministicCrypto())
  })

  afterEach(() => {
    clearTfCliImportSession()
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('verifies the linked tf session and signs the exact import body', async () => {
    setURL(`/keys#tf=1.43110.${sessionSecret}`)
    expect(initializeTfCliImportSession()).toBe(true)
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('/ping')) {
        expect(new URL(url).searchParams.get('challenge')).toBe(deterministicChallenge)
        return new Response(JSON.stringify({
          ok: true,
          service: 'tf',
          protocol: 1,
          version: 'dev',
          proof: deterministicPingProof,
        }), { status: 200 })
      }

      expect(url).toBe('http://127.0.0.1:43110/import')
      expect(init?.body).toBe('{"key":"sk-test","host":"https://tokenflux.dev","key_name":"laptop","version":1}')
      expect((init?.headers as Record<string, string>)['X-TF-Session-Proof'])
        .toBe(deterministicImportProof)
      expect(init).toMatchObject({
        method: 'POST',
        mode: 'cors',
        cache: 'no-store',
        credentials: 'omit',
        redirect: 'error',
        referrerPolicy: 'no-referrer',
        targetAddressSpace: 'loopback',
      })
      return new Response(JSON.stringify({ ok: true }), { status: 202 })
    })
    vi.stubGlobal('fetch', fetchMock)

    const target = await findTfCli()

    expect(target).toEqual({ port: 43110, verified: true })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const pingInit = fetchMock.mock.calls[0]?.[1] as RequestInit & { targetAddressSpace?: string }
    expect(pingInit).toMatchObject({
      method: 'GET',
      mode: 'cors',
      cache: 'no-store',
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      targetAddressSpace: 'loopback',
    })

    const result = await importKeyToTf(target!, {
      key: 'sk-test',
      host: 'https://tokenflux.dev',
      key_name: 'laptop',
    })

    expect(result).toEqual({ ok: true, status: 202 })
    expect(initializeTfCliImportSession()).toBe(false)
  })

  it('does not send a key after the verified session proof expires', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-03T00:00:00Z'))
    setURL(`/keys#tf=1.43110.${sessionSecret}`)
    expect(initializeTfCliImportSession()).toBe(true)
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true,
      service: 'tf',
      protocol: 1,
      proof: deterministicPingProof,
    }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const target = await findTfCli()
    expect(target).toEqual({ port: 43110, verified: true })

    await vi.advanceTimersByTimeAsync(10 * 60_000)
    await expect(importKeyToTf(target!, {
      key: 'sk-test',
      host: 'https://tokenflux.dev',
    })).resolves.toEqual({ ok: false, error: 'session_expired' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('does not send the key when import proof signing fails', async () => {
    setURL(`/keys#tf=1.43110.${sessionSecret}`)
    expect(initializeTfCliImportSession()).toBe(true)
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true,
      service: 'tf',
      protocol: 1,
      proof: deterministicPingProof,
    }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const target = await findTfCli()
    expect(target).toEqual({ port: 43110, verified: true })
    vi.spyOn(webcrypto.subtle, 'sign').mockRejectedValueOnce(new Error('Web Crypto unavailable'))

    await expect(importKeyToTf(target!, {
      key: 'sk-test',
      host: 'https://tokenflux.dev',
    })).resolves.toEqual({ ok: false, error: 'session_proof_unavailable' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps a responding linked service usable but unverified when its proof is wrong', async () => {
    setURL(`/keys#tf=1.43110.${sessionSecret}`)
    expect(initializeTfCliImportSession()).toBe(true)
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true,
      service: 'tf',
      protocol: 1,
      proof: 'wrong-proof',
    }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(findTfCli()).resolves.toEqual({ port: 43110, verified: false })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('discovers an unlinked tf service without sending a session proof', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith(':43113/ping')) {
        return new Response(JSON.stringify({ ok: true, service: 'tf', protocol: 1 }), { status: 200 })
      }
      if (url.endsWith(':43113/import')) {
        expect((init?.headers as Record<string, string>)['X-TF-Session-Proof']).toBeUndefined()
        return new Response(JSON.stringify({ ok: false, error: 'rejected' }), { status: 409 })
      }
      return new Response(JSON.stringify({ ok: false }), { status: 404 })
    })
    vi.stubGlobal('fetch', fetchMock)

    const target = await findTfCli()

    expect(target).toEqual({ port: 43113, verified: false })
    await expect(importKeyToTf(target!, {
      key: 'sk-test',
      host: 'https://tokenflux.dev',
    })).resolves.toEqual({ ok: false, status: 409, error: 'rejected' })
  })

  it('rejects services that do not identify as tf protocol v1', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true,
      service: 'other',
      protocol: 1,
    }), { status: 200 })))

    await expect(findTfCli(undefined, 1_000)).resolves.toBeNull()
  })
})
