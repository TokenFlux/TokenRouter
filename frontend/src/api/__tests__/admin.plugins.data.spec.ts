import { beforeEach, describe, expect, it, vi } from 'vitest'
import { backupData } from '../admin/plugins'

// admin.plugins.data.spec.ts verifies binary archive handling across policy errors.
// A Blob response retains administrator verification metadata, while successful
// archives are returned without envelope interpretation or content conversion
// by the browser API client and download controller.

// The client fixture supplies completed responses and records status-validation settings without contacting the host.
const fixture = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get: fixture.get } }))

beforeEach(() => {
  vi.resetAllMocks()
})

describe('plugin data downloads', () => {
  // An administrator downloads a private archive before completing required TOTP verification.
  // The JSON error arrives as a Blob and keeps its reason so the standard verification controller can retry the download.
  it('retains the step-up reason from a binary policy response', async () => {
    fixture.get.mockResolvedValue({
      status: 403,
      data: new Blob(
        [JSON.stringify({ reason: 'STEP_UP_REQUIRED', message: 'Verification required' })],
        { type: 'application/json' },
      ),
    })
    await expect(backupData('test.plugin')).rejects.toMatchObject({
      status: 403,
      reason: 'STEP_UP_REQUIRED',
    })
  })

  // An authorized download returns ZIP bytes after the server creates a stopped-owner snapshot.
  // The same Blob reaches the save operation, and native authentication failures remain eligible for the shared client's refresh handling.
  it('preserves archive bytes and ordinary authentication error handling', async () => {
    const archive = new Blob(['zip bytes'], { type: 'application/zip' })
    fixture.get.mockResolvedValue({ status: 200, data: archive })
    await expect(backupData('test.plugin')).resolves.toBe(archive)
    const options = fixture.get.mock.calls[0]![1] as { validateStatus: (status: number) => boolean }
    expect(options.validateStatus(401)).toBe(false)
    expect(options.validateStatus(403)).toBe(true)
  })
})
