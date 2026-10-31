import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { usePluginAdministration } from '../usePluginAdministration'
import { installationFixture } from './fixtures'

// usePluginAdministration.spec.ts controls overlapping reads and lifecycle writes.
// The real page controller owns Vue scope cleanup while transport and step-up
// fixtures expose publication order, mutation serialization, and readback after
// uncertain failures without touching persistent installation records.

// Deferred actions and list reads remain owned by each test; unmount removes the
// polling timer and settles verification before mocks are reset for another scope.
const fixture = vi.hoisted(() => ({
  list: vi.fn(),
  refresh: vi.fn(),
  run: vi.fn(),
  cancel: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({ list: fixture.list }))
vi.mock('@/stores/plugins', () => ({ usePluginsStore: () => ({ refreshUi: fixture.refresh }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: fixture.cancel }),
}))

/** controllerScope mounts the real controller and returns its page-owned observable state. */
function controllerScope() {
  let controller!: ReturnType<typeof usePluginAdministration>
  const wrapper = mount(
    defineComponent({
      setup() {
        controller = usePluginAdministration()
        return () => h('div')
      },
    }),
  )
  wrappers.push(wrapper)
  return { wrapper, controller }
}

beforeEach(() => {
  vi.clearAllMocks()
  fixture.list.mockResolvedValue([])
  fixture.refresh.mockResolvedValue([])
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('installed plugin administration ownership', () => {
  // The initial list request remains pending when a lifecycle mutation completes.
  // A later post-mutation read owns publication, so the older enabled observation cannot replace the confirmed disabled record.
  it('rejects pre-mutation list completion after readback', async () => {
    let finish!: (items: ReturnType<typeof installationFixture>[]) => void
    fixture.list.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const disabled = { ...installationFixture(), enabled: false }
    fixture.list.mockResolvedValueOnce([disabled])
    const { controller } = controllerScope()
    await controller.run(async () => 'saved')
    expect(controller.installations.value).toEqual([disabled])
    finish([installationFixture()])
    await flushPromises()
    expect(controller.installations.value[0]?.enabled).toBe(false)
    expect(fixture.refresh).toHaveBeenCalledTimes(1)
  })

  // A transport rejection cannot establish whether the backend persisted part of an action.
  // The controller refreshes installation and menu state while preserving the original failure for its caller.
  it('reconciles observations after an uncertain mutation failure', async () => {
    const { controller } = controllerScope()
    await flushPromises()
    const failure = new Error('Connection lost')
    await expect(
      controller.run(async () => {
        throw failure
      }),
    ).rejects.toBe(failure)
    expect(fixture.list).toHaveBeenCalledTimes(2)
    expect(fixture.refresh).toHaveBeenCalledTimes(1)
    expect(controller.busy.value).toBe(false)
  })

  // One pending action owns the sole verification controller while another activation arrives.
  // The second action is never invoked, and unmount prevents the first completion from starting new list or menu reads.
  it('serializes actions and stops reconciliation after unmount', async () => {
    const { controller, wrapper } = controllerScope()
    await flushPromises()
    let finish!: () => void
    const first = controller.run(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve
        }),
    )
    const second = vi.fn(async () => undefined)
    await expect(controller.run(second)).rejects.toThrow('pending')
    expect(second).not.toHaveBeenCalled()
    wrapper.unmount()
    finish()
    await first
    expect(fixture.list).toHaveBeenCalledTimes(1)
    expect(fixture.refresh).not.toHaveBeenCalled()
    expect(fixture.cancel).toHaveBeenCalled()
  })
})
