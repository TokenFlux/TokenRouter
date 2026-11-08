import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import ImageUpload from '../ImageUpload.vue'
import { createI18n } from 'vue-i18n'

// ImageUpload.spec.ts exercises draft selection and asynchronous image validation.
// Controlled browser decoders expose late callbacks, ensuring removal or a newer
// selection cannot be overwritten. File-type and byte limits protect the existing
// draft before reading, without requiring a network upload or actual image decoder.

enableAutoUnmount(afterEach)
afterEach(() => vi.unstubAllGlobals())

/** choose supplies one file to the native picker and dispatches its change event. */
async function choose(wrapper: ReturnType<typeof mount>, file: File) {
  const input = wrapper.get('input[type="file"]')
  Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
  await input.trigger('change')
}

/** mountCover creates a constrained cover editor with a previously saved draft. */
function mountCover() {
  return mount(ImageUpload, {
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: {} } })] },
    props: {
      modelValue: 'saved-cover',
      allowedTypes: ['image/png', 'image/jpeg', 'image/webp'],
      maxSize: 2 * 1024 * 1024,
      maxDimension: 4096,
      wide: true,
    },
  })
}

describe('cover uploads', () => {
  // Unsupported SVG and oversized data are selected over an existing cover.
  // Both failures must preserve the draft instead of publishing invalid file contents.
  it('rejects unsupported formats and oversized files', async () => {
    const wrapper = mountCover()
    await choose(wrapper, new File(['<svg/>'], 'cover.svg', { type: 'image/svg+xml' }))
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await choose(
      wrapper,
      new File([new Uint8Array(2 * 1024 * 1024 + 1)], 'cover.png', { type: 'image/png' }),
    )
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.get('img').attributes('src')).toBe('saved-cover')
  })

  // Image decoding completes after another selection and after removal. Only the
  // current selection may update the parent, preventing a removed cover from reappearing.
  it('validates dimensions and ignores obsolete decoder callbacks', async () => {
    // Browser substitutes retain asynchronous callbacks so completion order is explicit.
    const readers: Array<{ onload: (() => void) | null; result: string }> = []
    const images: Array<{
      onload: (() => void) | null
      naturalWidth: number
      naturalHeight: number
    }> = []
    vi.stubGlobal(
      'FileReader',
      class {
        onload: (() => void) | null = null // Captured completion callback.
        result = 'data:image/png;base64,fixture' // Encoded fixture contents.
        constructor() {
          readers.push(this)
        }
        /** readAsDataURL leaves completion under the test's explicit control. */
        readAsDataURL() {}
      },
    )
    vi.stubGlobal(
      'Image',
      class {
        onload: (() => void) | null = null // Captured decoder callback.
        naturalWidth = 1200 // Decoded width in pixels.
        naturalHeight = 630 // Decoded height in pixels.
        constructor() {
          images.push(this)
        }
      },
    )
    const wrapper = mountCover()
    const file = new File(['png'], 'cover.png', { type: 'image/png' })
    await choose(wrapper, file)
    readers[0].onload!()
    images[0].naturalWidth = 4097
    images[0].onload!()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await choose(wrapper, file)
    readers[1].onload!()
    images[1].onload!()
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['data:image/png;base64,fixture'])
    await choose(wrapper, file)
    readers[2].onload!()
    await wrapper.get('button').trigger('click')
    images[2].onload!()
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')).toEqual([['data:image/png;base64,fixture'], ['']])
  })
  // A parent restores its draft during either file reading or image decoding.
  // Resets must invalidate callbacks even when the restored value equals the previous value.
  it.each([
    ['changed value', 'reading'],
    ['changed value', 'decoding'],
    ['same-value reload', 'reading'],
    ['same-value reload', 'decoding'],
  ])('cancels %s during %s and accepts a later selection', async (reset, stage) => {
    // Controlled browser callbacks preserve the order of reset and completion.
    const readers: Array<{ onload: (() => void) | null }> = []
    const images: Array<{ onload: (() => void) | null }> = []
    vi.stubGlobal(
      'FileReader',
      class {
        onload: (() => void) | null = null // Captured file-read completion.
        result = 'data:image/png;base64,new-cover' // Encoded replacement cover.
        /** constructor registers this pending read for deterministic completion control. */
        constructor() {
          readers.push(this)
        }
        /** readAsDataURL waits for the test to complete this read. */
        readAsDataURL() {}
      },
    )
    vi.stubGlobal(
      'Image',
      class {
        onload: (() => void) | null = null // Captured image-decode completion.
        naturalWidth = 1200 // Decoded width in pixels.
        naturalHeight = 630 // Decoded height in pixels.
        /** constructor registers this decoder for completion after the parent reset. */
        constructor() {
          images.push(this)
        }
      },
    )
    const wrapper = mountCover()
    const file = new File(['png'], 'cover.png', { type: 'image/png' })
    await choose(wrapper, file)
    if (stage === 'decoding') readers[0].onload!()
    if (reset === 'changed value') {
      await wrapper.setProps({ modelValue: 'restored-cover' })
    } else {
      await wrapper.setProps({ disabled: true })
      expect((wrapper.get('input').element as HTMLInputElement).disabled).toBe(true)
      await choose(wrapper, file)
      expect(readers).toHaveLength(1)
      await wrapper.setProps({ modelValue: 'saved-cover', disabled: false })
    }
    if (stage === 'reading') readers[0].onload!()
    else images[0].onload!()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await choose(wrapper, file)
    readers[1].onload!()
    images.at(-1)!.onload!()
    expect(wrapper.emitted('update:modelValue')).toEqual([['data:image/png;base64,new-cover']])
  })
})
