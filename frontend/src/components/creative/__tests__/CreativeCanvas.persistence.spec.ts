import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CreativeCanvas from '@/components/creative/CreativeCanvas.vue'
import { outputAssetKey, type LocalAsset } from '@/utils/creativeLocalStore'

/**
 * CreativeCanvas.persistence.spec.ts exercises scene recovery and snapshot
 * writes through the mounted canvas component with controlled storage promises.
 * A lightweight Fabric substitute records loadFromJSON input and inserted
 * images, allowing the suite to check restore-versus-placement ordering, image
 * placeholder resolution, and cleanup of temporary poster URLs. It also
 * verifies that incomplete or missing-asset recovery does not overwrite saved
 * data.
 */

/**
 * fabricState retains mock canvas instances and decoded images so recovery
 * ordering and passed snapshot metadata remain observable.
 */
const fabricState = vi.hoisted(() => ({
  main: null as any,
  mask: null as any,
  images: [] as any[],
}))

/**
 * storeMocks supplies delayed scene reads and records writes, separating
 * lifecycle assertions from browser IndexedDB implementation details.
 */
const storeMocks = vi.hoisted(() => ({
  saveAsset: vi.fn(),
  loadAsset: vi.fn(),
  loadSceneJson: vi.fn(),
  saveSceneJson: vi.fn(),
}))

/** Playback substitutes expose surface ownership without decoding pixels in component tests. */
const playbackMocks = vi.hoisted(() => ({ create: vi.fn(), start: vi.fn(), dispose: vi.fn() }))
vi.mock('../gifPlayback', () => ({ createGIFPlayback: playbackMocks.create }))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('@/utils/creativeLocalStore', () => ({
  /**
   * LocalStoreQuotaError supplies the storage error identity used by the canvas
   * fixture.
   */
  LocalStoreQuotaError: class LocalStoreQuotaError extends Error {},
  loadAsset: storeMocks.loadAsset,
  loadSceneJson: storeMocks.loadSceneJson,
  saveAsset: storeMocks.saveAsset,
  saveSceneJson: storeMocks.saveSceneJson,
  localAssetKey: (kind: string, id: string) => `${kind}:local:${id}`,
  outputAssetKey: (runId: string, outputIndex: number) => `output:${runId}:${outputIndex}`,
}))

vi.mock('fabric', () => {
  /**
   * MockObject provides mutable scene geometry for the component's Fabric
   * interactions.
   */
  class MockObject {
    left = 0 // left scene coordinate
    top = 0 // top scene coordinate
    width = 0 // horizontal dimension pixels
    height = 0 // vertical dimension pixels
    scaleX = 1 // horizontal display scale
    scaleY = 1 // vertical display scale
    angle = 0 // object rotation degrees
    data?: Record<string, unknown> // custom scene object metadata
    selectable = true // object selection eligibility
    evented = true // object event eligibility

    /**
     * set applies fixture properties to this object and returns the same instance.
     */
    set(values: Record<string, unknown>): this {
      Object.assign(this, values)
      return this
    }

    /**
     * setCoords supplies a no-op coordinate refresh for the simplified Fabric
     * fixture.
     */
    setCoords(): void {}

    /** calcTransformMatrix maps centered image coordinates into the fixture scene. */
    calcTransformMatrix(): [number, number, number, number, number, number] {
      return [
        this.scaleX,
        0,
        0,
        this.scaleY,
        this.left + (this.width * this.scaleX) / 2,
        this.top + (this.height * this.scaleY) / 2,
      ]
    }

    /**
     * getBoundingRect returns translated dimensions scaled by the fixture's
     * horizontal and vertical factors.
     */
    getBoundingRect(): {
      left: number // left scene coordinate
      top: number // top scene coordinate
      width: number // horizontal dimension pixels
      height: number // vertical dimension pixels
    } {
      return {
        left: this.left,
        top: this.top,
        width: this.width * this.scaleX,
        height: this.height * this.scaleY,
      }
    }
  }

  /**
   * MockImage supplies fixed image dimensions and records simulated decoding
   * results.
   */
  class MockImage extends MockObject {
    width = 400 // horizontal dimension pixels
    height = 200 // vertical dimension pixels
    element: HTMLImageElement | HTMLCanvasElement = Object.assign(document.createElement('img'), {
      width: 400,
      height: 200,
    }) // current fixture pixel source

    /**
     * getElement returns the current poster or animation source for playback assertions.
     */
    getElement(): HTMLImageElement | HTMLCanvasElement {
      return this.element
    }

    /** setElement replaces the rendered source while retaining fixture scene geometry. */
    setElement(element: HTMLImageElement | HTMLCanvasElement): void {
      this.element = element
    }

    /**
     * fromURL creates a decoded image fixture and records it for placement
     * assertions. URL bytes are not decoded in this substitute, keeping the test
     * focused on component ordering and metadata.
     */
    static async fromURL(): Promise<MockImage> {
      const image = new MockImage()
      fabricState.images.push(image)
      return image
    }
  }

  /**
   * MockCanvas provides the scene and viewport operations exercised by mounted
   * canvas tests.
   */
  class MockCanvas {
    upperCanvasEl = document.createElement('canvas') // canvas interaction element
    viewportTransform: [number, number, number, number, number, number] = [1, 0, 0, 1, 0, 0] // current affine viewport transform
    width = 800 // horizontal dimension pixels
    height = 600 // vertical dimension pixels
    backgroundColor = '' // canvas background color
    objects: MockObject[] = [] // ordered shared scene objects
    active: MockObject | null = null // currently selected fixture object
    loadedJSON: Record<string, unknown> | null = null // last supplied scene snapshot
    handlers = new Map<string, (event: any) => void>() // registered scene event callbacks

    /**
     * constructor registers this fixture as the main canvas or its mask overlay.
     * The first instance occupies the main slot, and the next instance lets tests
     * inspect overlay behavior separately.
     */
    constructor() {
      if (!fabricState.main) fabricState.main = this
      else fabricState.mask = this
    }

    /**
     * on retains scene callbacks so tests can reproduce render and removal events.
     */
    on(name: string, handler: (event: any) => void): void {
      this.handlers.set(name, handler)
    }
    /**
     * getWidth returns the fixture viewport width used by canvas placement.
     */
    getWidth(): number {
      return this.width
    }
    /**
     * getHeight returns the fixture viewport height used by canvas placement.
     */
    getHeight(): number {
      return this.height
    }
    /**
     * getZoom reads the horizontal scale from the fixture viewport transform.
     */
    getZoom(): number {
      return this.viewportTransform[0]
    }
    /**
     * setViewportTransform stores the supplied transform tuple as the fixture
     * viewport state. The tuple remains shared, matching the direct mutations
     * inspected by these tests rather than creating an independent copy.
     */
    setViewportTransform(value: [number, number, number, number, number, number]): void {
      this.viewportTransform = value
    }
    /**
     * setDimensions updates the fixture viewport size from the supplied width and
     * height.
     */
    setDimensions(value: {
      width: number // horizontal dimension pixels
      height: number // vertical dimension pixels
    }): void {
      this.width = value.width
      this.height = value.height
    }
    /**
     * requestRenderAll refreshes overlay positions without drawing fixture pixels.
     */
    requestRenderAll(): void {
      this.handlers.get('after:render')?.({})
    }
    /**
     * add appends shared scene objects to the fixture's ordered object list.
     */
    add(...objects: MockObject[]): void {
      this.objects.push(...objects)
    }
    /**
     * remove filters specified object identities from the fixture scene list.
     */
    remove(...objects: MockObject[]): void {
      this.objects = this.objects.filter((object) => !objects.includes(object))
      for (const target of objects) this.handlers.get('object:removed')?.({ target })
    }
    /**
     * setActiveObject stores one fixture object as the current canvas selection.
     */
    setActiveObject(object: MockObject): void {
      this.active = object
    }
    /**
     * getActiveObject returns the shared selected object or null when selection is
     * empty. The component can inspect its identity and metadata; clearing
     * selection leaves the object in the scene list.
     */
    getActiveObject(): MockObject | null {
      return this.active
    }
    /**
     * getActiveObjects wraps the selected fixture object in a new array when
     * present. An empty selection returns an empty array, while contained object
     * identities remain shared with the scene.
     */
    getActiveObjects(): MockObject[] {
      return this.active ? [this.active] : []
    }
    /**
     * discardActiveObject clears the fixture selection while retaining all scene
     * objects.
     */
    discardActiveObject(): void {
      this.active = null
    }
    /**
     * getObjects returns the fixture's shared array of current scene objects. The
     * component reads and filters these identities directly, so callers needing an
     * isolated list must create their own copy.
     */
    getObjects(): MockObject[] {
      return this.objects
    }
    /**
     * toObject serializes fixture image positions, scales, and custom metadata into
     * a scene object. Nested metadata remains shared in this temporary
     * representation, and production snapshot serialization converts it to JSON
     * text.
     */
    toObject(): {
      objects: Array<Record<string, unknown>> // ordered shared scene objects
    } {
      return {
        objects: this.objects.map((object) => ({
          type: 'Image',
          left: object.left,
          top: object.top,
          scaleX: object.scaleX,
          scaleY: object.scaleY,
          data: object.data,
        })),
      }
    }
    /**
     * loadFromJSON records the restored snapshot and clears the fixture object
     * list. The substitute resolves immediately, allowing tests to observe ordering
     * around the separate delayed storage read.
     */
    async loadFromJSON(json: Record<string, unknown>): Promise<this> {
      this.loadedJSON = json
      this.objects = []
      return this
    }
    /**
     * clear empties the fixture scene list for reset and restoration assertions.
     */
    clear(): void {
      this.objects = []
    }
    /**
     * dispose accepts component cleanup without allocating browser drawing
     * resources in the fixture.
     */
    dispose(): void {}
  }

  /**
   * MockBrush stores drawing properties used when the component enables painting.
   */
  class MockBrush {
    color = '' // configured brush color
    width = 0 // horizontal dimension pixels
  }

  /**
   * MockPoint carries viewport coordinates passed to the Fabric zoom substitute.
   */
  class MockPoint {
    /**
     * constructor stores the horizontal and vertical coordinates of a fixture
     * point.
     */
    constructor(
      public x: number, // horizontal point coordinate
      public y: number, // vertical point coordinate
    ) {}

    /** transform returns a new point after applying the supplied affine matrix. */
    transform(matrix: number[]): MockPoint {
      return new MockPoint(
        matrix[0] * this.x + matrix[2] * this.y + matrix[4],
        matrix[1] * this.x + matrix[3] * this.y + matrix[5],
      )
    }
  }

  return {
    Canvas: MockCanvas,
    FabricImage: MockImage,
    InteractiveFabricObject: { ownDefaults: {} },
    PencilBrush: MockBrush,
    Point: MockPoint,
    Rect: MockObject,
    StaticCanvas: MockCanvas,
    /**
     * MockText represents runtime resolution labels within the lightweight Fabric
     * fixture.
     */
    Text: class MockText extends MockObject {
      text: string // displayed fixture label text
      /**
       * constructor records label text on the lightweight runtime tag fixture.
       */
      constructor(text: string) {
        super()
        this.text = text
      }
    },
  }
})

/**
 * deferred creates a manually resolved promise for controlling an asynchronous
 * test interleaving. The test owns its resolver and releases the pending
 * operation after asserting the intermediate state.
 */
function deferred<T>(): {
  promise: Promise<T> // controlled asynchronous test result
  resolve: (value: T) => void // test-owned promise completion callback
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

/**
 * mountCanvas mounts the real canvas component with crop and icon children
 * stubbed. Transitions are stubbed too, so a replaced GIF control leaves the DOM
 * immediately instead of lingering until a later animation frame that
 * flushPromises does not await. It uses generation mode by default; the caller
 * owns unmounting to exercise listener and resource cleanup.
 */
function mountCanvas() {
  return mount(CreativeCanvas, {
    props: { operation: 'generate' },
    global: { stubs: { CropperModal: true, Icon: true, transition: true } },
  })
}

describe('CreativeCanvas 本地恢复与快照持久化', () => {
  /**
   * A generated GIF keeps its original bytes while the canvas receives a PNG
   * poster. Playing replaces the existing image surface, and stopping restores
   * the poster without changing original downloads or persistent asset references.
   */
  it('keeps GIF bytes for playback while rendering a separate poster', async () => {
    const contextSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockReturnValue({ drawImage: vi.fn() } as unknown as CanvasRenderingContext2D)
    const poster = new Blob(['poster'], { type: 'image/png' })
    const encodeSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation((callback) => callback(poster))
    const wrapper = mountCanvas()
    await flushPromises()
    const animation = new Blob(['GIF89a'], { type: 'image/gif' })
    await (wrapper.vm as any).placeOutput({ blob: animation, runId: 'gif-run', outputIndex: 0 })
    await flushPromises()
    expect(URL.createObjectURL).toHaveBeenCalledWith(poster)
    expect(await (wrapper.vm as any).getSelectedImageBlob()).toBe(animation)
    expect(fabricState.images[0].data.animatedGIF).toBe(true)
    const image = fabricState.images[0]
    const originalElement = image.getElement()
    expect(wrapper.find('.canvas-toolbar button[title="creative.canvas.playGIF"]').exists()).toBe(
      false,
    )
    await wrapper.get('button[title="creative.canvas.playGIF"]').trigger('click')
    await flushPromises()
    expect(playbackMocks.start).toHaveBeenCalledOnce()
    expect(image.getElement()).toBeInstanceOf(HTMLCanvasElement)
    expect(await (wrapper.vm as any).getSelectedImageBlob()).toBe(animation)
    const control = wrapper.get('button[title="creative.canvas.stopGIF"]')
    expect(control.classes()).toContain('canvas-gif-toggle')
    const beforeMove = control.attributes('style')
    image.left += 80
    fabricState.main.requestRenderAll()
    await flushPromises()
    expect(control.attributes('style')).not.toBe(beforeMove)
    await control.trigger('click')
    expect(image.getElement()).toBe(originalElement)
    expect(playbackMocks.dispose).toHaveBeenCalledOnce()
    await wrapper.get('button[title="creative.canvas.playGIF"]').trigger('click')
    await flushPromises()
    wrapper.unmount()
    expect(playbackMocks.dispose).toHaveBeenCalledTimes(2)
    expect(image.getElement()).toBe(originalElement)
    contextSpy.mockRestore()
    encodeSpy.mockRestore()
  })

  /**
   * A decoder finishes after the user selects another image. The obsolete
   * player must be disposed without replacing either image's poster or starting
   * a timer for a selection that no longer owns playback.
   */
  it('disposes a late GIF player after selection changes', async () => {
    const contextSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockReturnValue({ drawImage: vi.fn() } as unknown as CanvasRenderingContext2D)
    const encodeSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation((callback) => callback(new Blob(['poster'], { type: 'image/png' })))
    const pending = deferred<any>()
    playbackMocks.create.mockReturnValueOnce(pending.promise)
    const wrapper = mountCanvas()
    await flushPromises()
    await (wrapper.vm as any).placeOutput({
      blob: new Blob(['GIF89a'], { type: 'image/gif' }),
      runId: 'gif-run',
      outputIndex: 0,
    })
    await flushPromises()
    const originalElement = fabricState.images[0].getElement()
    await wrapper.get('button[title="creative.canvas.playGIF"]').trigger('click')
    await flushPromises()
    await (wrapper.vm as any).addUploadedImage(new Blob(['static'], { type: 'image/png' }))
    await flushPromises()
    pending.resolve({
      surface: document.createElement('canvas'),
      start: playbackMocks.start,
      dispose: playbackMocks.dispose,
    })
    await flushPromises()
    expect(playbackMocks.start).not.toHaveBeenCalled()
    expect(playbackMocks.dispose).toHaveBeenCalledOnce()
    expect(fabricState.images[0].getElement()).toBe(originalElement)
    expect(wrapper.find('.canvas-gif-toggle').exists()).toBe(false)
    wrapper.unmount()
    contextSpy.mockRestore()
    encodeSpy.mockRestore()
  })

  beforeEach(() => {
    playbackMocks.start.mockReset()
    playbackMocks.dispose.mockReset()
    playbackMocks.create.mockReset().mockImplementation(async () => ({
      surface: Object.assign(document.createElement('canvas'), { width: 400, height: 200 }),
      start: playbackMocks.start,
      dispose: playbackMocks.dispose,
    }))
    fabricState.main = null
    fabricState.mask = null
    fabricState.images = []
    storeMocks.saveAsset.mockReset().mockResolvedValue(undefined)
    storeMocks.loadAsset.mockReset().mockResolvedValue(null)
    storeMocks.loadSceneJson.mockReset().mockResolvedValue(null)
    storeMocks.saveSceneJson.mockReset().mockResolvedValue(undefined)
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:creative-persistence-test'),
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    })
    vi.stubGlobal(
      'createImageBitmap',
      vi.fn(async () => ({
        width: 400,
        height: 200,
        close: vi.fn(),
      })),
    )
  })

  /**
   * A scene read remains unresolved when the component unmounts. Neither teardown
   * nor its later completion may save the temporarily empty canvas over the
   * existing snapshot.
   */
  it('恢复未完成时卸载不会用空画布覆盖已有快照', async () => {
    const pending = deferred<string | null>()
    storeMocks.loadSceneJson.mockReturnValue(pending.promise)
    const wrapper = mountCanvas()

    wrapper.unmount()
    expect(storeMocks.saveSceneJson).not.toHaveBeenCalled()

    pending.resolve(JSON.stringify({ objects: [] }))
    await flushPromises()
    expect(storeMocks.saveSceneJson).not.toHaveBeenCalled()
  })

  /**
   * An output arrives while the initial scene read is still pending. Its
   * placement must wait until restoration completes so the loaded scene cannot
   * erase that insertion.
   */
  it('恢复期间上板会等待 loadFromJSON 完成，最终对象不会被清掉', async () => {
    const pending = deferred<string | null>()
    storeMocks.loadSceneJson.mockReturnValue(pending.promise)
    const wrapper = mountCanvas()
    const output = {
      blob: new Blob(['output'], { type: 'image/png' }),
      runId: 'crun_0123456789abcdef',
      outputIndex: 0,
    }

    const placement = (wrapper.vm as any).placeOutput(output)
    expect(fabricState.images).toHaveLength(0)

    pending.resolve(JSON.stringify({ objects: [] }))
    await placement

    expect(fabricState.images).toHaveLength(1)
    wrapper.unmount()
  })

  /**
   * A stored scene contains an asset placeholder and viewport metadata. Recovery
   * must supply a temporary image URL to Fabric, retain that metadata, and revoke
   * the URL.
   */
  it('从 IndexedDB 恢复图片占位符并保留画布变换', async () => {
    const assetKey = outputAssetKey('crun_0123456789abcdef', 0)
    const asset: LocalAsset = {
      key: assetKey,
      kind: 'output',
      blob: new Blob(['output'], { type: 'image/png' }),
      runId: 'crun_0123456789abcdef',
      outputIndex: 0,
      createdAt: 1,
    }
    storeMocks.loadSceneJson.mockResolvedValue(
      JSON.stringify({
        viewportTransform: [1.25, 0, 0, 1.25, 32, 48],
        objects: [{ type: 'image', src: `asset://${assetKey}`, data: { kind: 'image', assetKey } }],
      }),
    )
    storeMocks.loadAsset.mockResolvedValue(asset)
    const wrapper = mountCanvas()
    await flushPromises()

    expect(fabricState.main.loadedJSON).toEqual(
      expect.objectContaining({
        viewportTransform: [1.25, 0, 0, 1.25, 32, 48],
      }),
    )
    expect((fabricState.main.loadedJSON?.objects as Array<Record<string, unknown>>)[0].src).toBe(
      'blob:creative-persistence-test',
    )
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:creative-persistence-test')
    wrapper.unmount()
  })

  /**
   * A source image is added before the page-hide event is dispatched. The
   * persisted scene must reference its local asset key when pending changes are
   * flushed.
   */
  it('页面隐藏时刷新有变更的快照', async () => {
    const wrapper = mountCanvas()
    await flushPromises()

    await (wrapper.vm as any).addUploadedImage(new Blob(['source'], { type: 'image/png' }))
    window.dispatchEvent(new Event('pagehide'))
    await flushPromises()

    expect(storeMocks.saveSceneJson).toHaveBeenCalledWith('creative:canvas', expect.any(String))
    const savedJson = storeMocks.saveSceneJson.mock.calls[0]?.[1] as string
    expect(savedJson).toContain('asset://source:local:')
    wrapper.unmount()
  })

  /**
   * A stored image references bytes absent from the local asset store. Recovery
   * must skip the image without saving a newly emptied snapshot over the existing
   * record.
   */
  it('本地素材缺失时只跳过图片，不保存被裁剪的空快照', async () => {
    const assetKey = outputAssetKey('crun_0123456789abcdef', 0)
    storeMocks.loadSceneJson.mockResolvedValue(
      JSON.stringify({
        objects: [{ type: 'image', src: `asset://${assetKey}`, data: { kind: 'image', assetKey } }],
      }),
    )
    storeMocks.loadAsset.mockResolvedValue(null)
    const wrapper = mountCanvas()
    await flushPromises()

    expect(storeMocks.saveSceneJson).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
