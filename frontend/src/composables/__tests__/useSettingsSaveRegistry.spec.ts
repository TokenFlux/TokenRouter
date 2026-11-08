import { describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { createSettingsSaveRegistry } from '@/composables/useSettingsSaveRegistry'

// useSettingsSaveRegistry.spec.ts checks page-level saving against independently owned
// section dirty flags. The cases observe registration order, false-return handling,
// unregistering, and selective discard so clean sections stay outside page-level
// persistence and reload operations.

describe('createSettingsSaveRegistry', () => {
  // Three registered targets include a clean middle section and a first save returning
  // false. Saving must call the dirty sections in order and return false after continuing.
  it('saves only dirty targets in registration order and keeps going after a failure', async () => {
    const { dirty, saveDirty, registry } = createSettingsSaveRegistry()
    const calls: string[] = []
    const firstDirty = ref(true)
    const secondDirty = ref(false)
    const thirdDirty = ref(true)
    registry.register('first', {
      dirty: firstDirty,
      save: vi.fn(async () => {
        calls.push('first')
        return false
      }),
      discard: vi.fn(async () => {}),
    })
    registry.register('second', {
      dirty: secondDirty,
      save: vi.fn(async () => {
        calls.push('second')
        return true
      }),
      discard: vi.fn(async () => {}),
    })
    registry.register('third', {
      dirty: thirdDirty,
      save: vi.fn(async () => {
        calls.push('third')
        return true
      }),
      discard: vi.fn(async () => {}),
    })

    expect(dirty.value).toBe(true)
    await expect(saveDirty()).resolves.toBe(false)
    expect(calls).toEqual(['first', 'third'])
  })

  // A registry contains one dirty section before that key is removed. The aggregate dirty
  // flag must change to false once the section is unregistered.
  it('stops tracking a target after it is unregistered', () => {
    const { dirty, registry } = createSettingsSaveRegistry()
    registry.register('only', { dirty: ref(true), save: async () => true, discard: async () => {} })
    expect(dirty.value).toBe(true)

    registry.unregister('only')
    expect(dirty.value).toBe(false)
  })

  // A registry contains both dirty and clean sections with separate discard spies. Discard
  // must invoke the dirty section exactly once and leave the clean section untouched.
  it('discards only dirty targets', async () => {
    const { discardDirty, registry } = createSettingsSaveRegistry()
    const dirtyDiscard = vi.fn(async () => {})
    const cleanDiscard = vi.fn(async () => {})
    registry.register('dirty', { dirty: ref(true), save: async () => true, discard: dirtyDiscard })
    registry.register('clean', { dirty: ref(false), save: async () => true, discard: cleanDiscard })

    await discardDirty()

    expect(dirtyDiscard).toHaveBeenCalledTimes(1)
    expect(cleanDiscard).not.toHaveBeenCalled()
  })
  // One section fails before another section finishes restoring its draft. Discard
  // must remain pending until every selected section settles, including synchronous failures.
  it.each(['synchronous', 'asynchronous'])(
    'waits for remaining reloads after a %s failure',
    async (mode) => {
      const { discardDirty, registry } = createSettingsSaveRegistry()
      const error = new Error('reload failed')
      let finishReload!: () => void
      const pending = new Promise<void>((resolve) => {
        finishReload = resolve
      })
      const slowReload = vi.fn(() => pending)
      registry.register('failed', {
        dirty: true,
        save: async () => true,
        discard: () => {
          if (mode === 'synchronous') throw error
          return Promise.reject(error)
        },
      })
      registry.register('slow', { dirty: true, save: async () => true, discard: slowReload })
      let finished = false
      const completion = discardDirty().catch((reason) => {
        finished = true
        return reason
      })
      await flushPromises()
      expect(slowReload).toHaveBeenCalledTimes(1)
      expect(finished).toBe(false)
      finishReload()
      await expect(completion).resolves.toBe(error)
      expect(finished).toBe(true)
    },
  )
})
