import { decompressFrame, parseGIF, type ParsedFrame } from 'gifuct-js'

/**
 * gifPlayback.ts composites GIF frames into a canvas that Fabric can render as
 * an ordinary image. Drawing an animated HTML image onto a canvas does not
 * supply reliable frame advancement, so playback decodes the original GIF and
 * applies each frame's pixels and disposal rule to one shared drawing surface.
 * Frame delays control redraws without continuously rendering an idle scene.
 *
 * Only the current frame patch and any restore-previous pixels remain decoded;
 * later frames are decoded when their turn arrives. Stopping cancels the timer
 * and releases the drawing buffers. The caller restores its independent poster
 * before disposal and keeps the original Blob for persistence and downloads.
 */

/** GIFPlayback exposes a temporary image surface with caller-owned playback lifetime. */
export interface GIFPlayback {
  surface: HTMLCanvasElement // composed animation frame surface
  /**
   * start draws the first frame and schedules subsequent frames using their
   * encoded delays. Repeated calls have no effect, and a disposed player cannot
   * be started again.
   */
  start: () => void
  /**
   * dispose cancels frame advancement and releases the temporary pixel buffers.
   * Detach the surface from its scene first; repeated disposal remains safe and
   * does not change the original Blob.
   */
  dispose: () => void
}

/**
 * createGIFPlayback prepares an animation without starting its timer. The caller
 * installs the returned surface into its scene before calling start, then
 * restores the poster before disposing it. Each composed frame invokes onFrame;
 * a later decode error cancels playback and invokes onError.
 *
 * Transparent frame patches blend with retained pixels. Disposal runs after
 * the preceding frame's delay: background disposal clears its rectangle, while
 * restore-previous disposal reinstates pixels captured before that patch. A
 * new loop resets the logical screen before drawing its first frame.
 */
export async function createGIFPlayback(
  blob: Blob,
  onFrame: () => void,
  onError: (error: unknown) => void,
): Promise<GIFPlayback> {
  const gif = parseGIF(await blob.arrayBuffer())
  const frames = gif.frames.filter((frame) => 'image' in frame)
  if (!frames.length || !gif.lsd.width || !gif.lsd.height) {
    throw new Error('GIF has no drawable frames')
  }
  const surface = document.createElement('canvas')
  surface.width = gif.lsd.width
  surface.height = gif.lsd.height
  const context = surface.getContext('2d')
  const patch = document.createElement('canvas')
  const patchContext = patch.getContext('2d')
  if (!context || !patchContext) throw new Error('GIF drawing context unavailable')

  let timer: ReturnType<typeof setTimeout> | null = null
  let disposed = false
  let started = false
  let previous: ParsedFrame | null = null
  let restoredPixels: ImageData | null = null
  const background = gif.gct?.[gif.lsd.backgroundColorIndex]

  /**
   * clearArea restores GIF background pixels within the supplied rectangle.
   * Transparent frames use a transparent background; opaque frames use the
   * logical screen's palette entry when one exists.
   */
  function clearArea(frame: ParsedFrame, left: number, top: number, width: number, height: number) {
    context!.clearRect(left, top, width, height)
    if (frame.transparentIndex === undefined && background) {
      context!.fillStyle = `rgb(${background.join(',')})`
      context!.fillRect(left, top, width, height)
    }
  }

  /**
   * drawFrame composites one patch after disposing its predecessor and schedules
   * the next frame using the encoded delay. The first frame resets the surface;
   * later frames preserve pixels outside the preceding disposal rectangle.
   * Only the current patch and its restore buffer survive between timers.
   * Scheduling follows composition, and a decode failure reports through onError
   * without scheduling another frame. Disposed players ignore queued callbacks
   * so a removed surface cannot restart playback.
   */
  function drawFrame(index: number): void {
    if (disposed) return
    try {
      const frame = decompressFrame(frames[index], gif.gct, true)
      if (index === 0) {
        clearArea(frame, 0, 0, surface.width, surface.height)
      } else if (previous?.disposalType === 2) {
        const { left, top, width, height } = previous.dims
        clearArea(previous, left, top, width, height)
      } else if (previous?.disposalType === 3 && restoredPixels) {
        context!.putImageData(restoredPixels, previous.dims.left, previous.dims.top)
      }

      const { left, top, width, height } = frame.dims
      // Restore-previous frames need the underlying pixels from before their
      // patch; retaining only that rectangle avoids copying the entire animation.
      restoredPixels =
        frame.disposalType === 3 ? context!.getImageData(left, top, width, height) : null
      patch.width = width
      patch.height = height
      const pixels = patchContext!.createImageData(width, height)
      pixels.data.set(frame.patch)
      patchContext!.putImageData(pixels, 0, 0)
      // Drawing a patch canvas preserves earlier pixels beneath transparent
      // patch pixels, whereas putting its image data directly would erase them.
      context!.drawImage(patch, left, top)
      previous = frame
      onFrame()
      // Match the decoder's 100 ms default when a GIF omits graphic control data.
      timer = setTimeout(() => drawFrame((index + 1) % frames.length), frame.delay || 100)
    } catch (error) {
      onError(error)
    }
  }

  return {
    surface,
    start: () => {
      if (started || disposed) return
      started = true
      drawFrame(0)
    },
    dispose: () => {
      disposed = true
      if (timer !== null) clearTimeout(timer)
      timer = null
      previous = null
      restoredPixels = null
      // Resetting the dimensions releases canvas pixel storage after the scene
      // has stopped referencing this temporary animation surface.
      surface.width = 0
      surface.height = 0
      patch.width = 0
      patch.height = 0
    },
  }
}
