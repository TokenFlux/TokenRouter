import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createGIFPlayback } from '../gifPlayback'

/**
 * gifPlayback.spec.ts verifies frame timing, disposal, and cleanup using a real
 * two-frame GIF with controlled graphic-control extensions. Canvas substitutes
 * record composition operations while the actual decoder reads compressed GIF
 * pixels, keeping timing assertions independent of browser rendering speed.
 */

/**
 * animationBlob supplies two real encoded frames with 40 and 90 ms delays.
 * The first frame's disposal value varies between tests. Only the Blob byte
 * reader is substituted because jsdom does not provide Blob.arrayBuffer.
 */
function animationBlob(disposal: number): Blob {
  const data = Uint8Array.from(
    atob('R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs='),
    (char) => char.charCodeAt(0),
  )
  const bytes = new Uint8Array([
    ...data.slice(0, 19),
    // Graphic-control delays use centiseconds, giving 40 ms for the first frame
    // and 90 ms for the second without changing their compressed image records.
    0x21,
    0xf9,
    4,
    disposal << 2,
    4,
    0,
    0,
    0,
    ...data.slice(19, 34),
    0x21,
    0xf9,
    4,
    0,
    9,
    0,
    0,
    0,
    ...data.slice(34),
  ])
  return { arrayBuffer: async () => bytes.buffer } as Blob
}

/** drawingContext records composition and supplies the pixel buffers used by GIF patches. */
function drawingContext() {
  return {
    clearRect: vi.fn(),
    fillRect: vi.fn(),
    drawImage: vi.fn(),
    putImageData: vi.fn(),
    getImageData: vi.fn(() => ({ data: new Uint8ClampedArray([1, 2, 3, 255]) })),
    createImageData: vi.fn((width: number, height: number) => ({
      data: new Uint8ClampedArray(width * height * 4),
    })),
    fillStyle: '',
  }
}

describe('GIF canvas playback', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  /**
   * Real encoded delays advance two decoded frames and then start a new loop.
   * Disposing the player cancels future redraws and releases its canvas dimensions.
   */
  it('uses encoded delays and cancels playback on disposal', async () => {
    const context = drawingContext()
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
      context as unknown as CanvasRenderingContext2D,
    )
    const redraw = vi.fn()
    const failure = vi.fn()
    const player = await createGIFPlayback(animationBlob(1), redraw, failure)
    player.start()
    expect(redraw).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(39)
    expect(redraw).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    expect(redraw).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(90)
    expect(redraw).toHaveBeenCalledTimes(3)
    expect(context.createImageData).toHaveBeenCalledWith(1, 1)
    player.dispose()
    vi.advanceTimersByTime(1000)
    expect(redraw).toHaveBeenCalledTimes(3)
    expect(player.surface.width).toBe(0)
    expect(failure).not.toHaveBeenCalled()
  })

  /**
   * Frame disposal applies after its display delay and before the next patch.
   * Background disposal clears its rectangle, while restore-previous reinstates
   * the saved underlying pixels rather than clearing the whole image.
   */
  it.each([2, 3])('applies disposal method %i before the following frame', async (disposal) => {
    const context = drawingContext()
    const patchContext = drawingContext()
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockReturnValueOnce(context as unknown as CanvasRenderingContext2D)
      .mockReturnValueOnce(patchContext as unknown as CanvasRenderingContext2D)
    const player = await createGIFPlayback(animationBlob(disposal), vi.fn(), vi.fn())
    player.start()
    context.clearRect.mockClear()
    vi.advanceTimersByTime(40)
    if (disposal === 2) {
      expect(context.clearRect).toHaveBeenCalledWith(0, 0, 1, 1)
    } else {
      expect(context.putImageData).toHaveBeenCalledWith(
        context.getImageData.mock.results[0].value,
        0,
        0,
      )
      expect(context.clearRect).not.toHaveBeenCalled()
    }
    player.dispose()
  })
})
