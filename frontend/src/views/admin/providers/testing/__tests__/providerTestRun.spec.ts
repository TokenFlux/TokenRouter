import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  createProviderTestRun,
  executeProviderTest,
  type ProviderTestRequestBody,
} from '../providerTestRun'

/**
 * providerTestRun.spec.ts checks image opt-in and immutable decision preview snapshots.
 * Local SSE fixtures complete real runner requests, allowing single and repeated batch-style
 * executions to be compared without calling a provider or relying on dialog layout.
 */

describe('decision image submission', () => {
  afterEach(() => vi.unstubAllGlobals())

  // Two runs share an editable image draft and receive nullable-confidence Box answers.
  // Requests explicitly enable images while private preview metadata stays in independent run snapshots.
  it('sends image opt-in and snapshots images for each run', async () => {
    const fetch = vi
      .fn()
      .mockImplementation(
        async () =>
          new Response(
            `data: ${JSON.stringify({ type: 'test_complete', success: true, data: { usage_valid: true, response: { model: 'jev-1.14.0', answers: { target: { type: 'box', box: { top: 0, bottom: 1, left: 0, right: 1 }, confidence: null } }, usage: { input_tokens: 1, output_tokens: 0 } } } })}\n`,
          ),
      )
    vi.stubGlobal('fetch', fetch)
    const body: ProviderTestRequestBody = {
      model_id: 'jev-latest',
      test_type: 'decision',
      systemone: {
        state: '<photo>',
        questions: { target: { type: 'box', instructions: 'Find the mug' } },
        assets: { '<photo>': { type: 'image', format: 'png', data_base64: 'aW1hZ2U=' } },
      },
      decisionPreview: {
        images: { '<photo>': { name: 'original.png', src: 'data:image/png;base64,aW1hZ2U=' } },
        boxImages: { target: '<photo>' },
      },
    }
    const runs = [createProviderTestRun(), createProviderTestRun()]
    for (const run of runs) {
      await executeProviderTest({
        providerId: 1,
        providerName: 'Jev',
        body,
        run,
        signal: new AbortController().signal,
        t: (key) => key,
      })
      expect(run.status).toBe('success')
      expect(run.decisionResult?.answers.target.confidence).toBeNull()
    }
    body.decisionPreview!.images['<photo>'].name = 'changed.png'
    body.systemone!.questions.target.instructions = 'Changed question'
    for (const run of runs) {
      expect(run.decisionPreview?.images['<photo>'].name).toBe('original.png')
      expect(run.decisionQuestions?.target.instructions).toBe('Find the mug')
    }
    for (const [, options] of fetch.mock.calls) {
      expect(options.headers['TypeSafe-Danger-Zone-Images']).toBe('true')
      expect(JSON.parse(options.body)).not.toHaveProperty('decisionPreview')
      expect(JSON.parse(options.body).systemone.assets['<photo>'].data_base64).toBe('aW1hZ2U=')
    }
  })

  // A text-only decision test uses the same runner without opting into image support.
  // The absence of image content does not add an experimental header to ordinary probes.
  it('leaves text-only decisions without image opt-in', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response('data: {"type":"test_complete","success":true}\n'))
    vi.stubGlobal('fetch', fetch)
    await executeProviderTest({
      providerId: 1,
      providerName: 'Jev',
      body: {
        model_id: 'jev-latest',
        test_type: 'decision',
        systemone: {
          state: 'ready',
          questions: { ready: { type: 'noul', instructions: 'Ready?' } },
        },
      },
      run: createProviderTestRun(),
      signal: new AbortController().signal,
      t: (key) => key,
    })
    expect(fetch.mock.calls[0][1].headers).not.toHaveProperty('TypeSafe-Danger-Zone-Images')
  })

  // An expired administrator access token can reject the initial SSE handshake with HTTP 401.
  // When a refresh token is stored, openProviderTestStream rotates credentials via /auth/refresh
  // once and retries the SSE stream with the replacement bearer token.
  it('refreshes auth token once and retries SSE stream on HTTP 401', async () => {
    localStorage.setItem('auth_token', 'expired-token')
    localStorage.setItem('refresh_token', 'valid-refresh')
    localStorage.setItem('auth_user', JSON.stringify({ id: 7 }))

    const axiosMod = await import('axios')
    const postSpy = vi.spyOn(axiosMod.default, 'post').mockResolvedValueOnce({
      data: {
        code: 0,
        data: {
          access_token: 'rotated-token',
          refresh_token: 'rotated-refresh',
          expires_in: 3600,
          token_type: 'Bearer',
        },
      },
    } as any)

    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response('Unauthorized', { status: 401 }))
      .mockResolvedValueOnce(new Response('data: {"type":"test_complete","success":true}\n'))
    vi.stubGlobal('fetch', fetch)

    const run = createProviderTestRun()
    await executeProviderTest({
      providerId: 9,
      providerName: 'OpenAI',
      body: { model_id: 'gpt-5' },
      run,
      signal: new AbortController().signal,
      t: (key) => key,
    })

    expect(run.status).toBe('success')
    expect(postSpy).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(fetch.mock.calls[0][1].headers.Authorization).toBe('Bearer expired-token')
    expect(fetch.mock.calls[1][1].headers.Authorization).toBe('Bearer rotated-token')
    localStorage.clear()
    postSpy.mockRestore()
  })
})
