import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ApiKey } from '@/types'
import TfCliImportDialog from '../TfCliImportDialog.vue'

const protocol = vi.hoisted(() => ({
  findTfCli: vi.fn(),
  importKeyToTf: vi.fn(),
}))

vi.mock('@/utils/tfCliImport', () => protocol)
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const apiKey = {
  id: 7,
  key: 'sk-secret',
  name: 'Laptop',
  group_id: 12,
  group: { id: 12, name: 'GPT' },
  is_composite: false,
} as ApiKey

function mountDialog(key: ApiKey = apiKey) {
  return mount(TfCliImportDialog, {
    props: {
      show: true,
      apiKey: key,
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title', 'width'],
          emits: ['close'],
          template: `
            <div v-if="show" data-test="base-dialog">
              <button data-test="base-close" @click="$emit('close')">close</button>
              <slot />
              <slot name="footer" />
            </div>
          `,
        },
        Icon: { template: '<span />' },
      },
    },
  })
}

describe('TfCliImportDialog', () => {
  beforeEach(() => {
    protocol.findTfCli.mockReset()
    protocol.importKeyToTf.mockReset()
  })

  it('requires a second click before sending to a verified tf session', async () => {
    protocol.findTfCli.mockResolvedValue({ port: 43110, verified: true })
    protocol.importKeyToTf.mockResolvedValue({ ok: true, status: 202 })
    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.find('[data-test="tf-cli-verified"]').exists()).toBe(true)
    expect(protocol.importKeyToTf).not.toHaveBeenCalled()

    await wrapper.get('[data-test="tf-cli-send"]').trigger('click')
    await flushPromises()

    expect(protocol.importKeyToTf).toHaveBeenCalledWith(
      { port: 43110, verified: true },
      {
        key: 'sk-secret',
        host: window.location.origin,
        key_name: 'Laptop',
        group_id: 12,
        group_name: 'GPT',
      },
      expect.any(AbortSignal),
    )
    expect(wrapper.find('[data-test="tf-cli-accepted"]').exists()).toBe(true)
  })

  it('requires another click after a verified proof becomes unavailable', async () => {
    protocol.findTfCli.mockResolvedValue({ port: 43110, verified: true })
    protocol.importKeyToTf
      .mockResolvedValueOnce({ ok: false, error: 'session_proof_unavailable' })
      .mockResolvedValueOnce({ ok: true, status: 202 })
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get('[data-test="tf-cli-send"]').trigger('click')
    await flushPromises()

    expect(protocol.importKeyToTf).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="tf-cli-unverified"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="tf-cli-accepted"]').exists()).toBe(false)

    await wrapper.get('[data-test="tf-cli-send"]').trigger('click')
    await flushPromises()

    expect(protocol.importKeyToTf).toHaveBeenCalledTimes(2)
    expect(protocol.importKeyToTf.mock.calls[1]?.[0]).toEqual({ port: 43110, verified: false })
    expect(wrapper.find('[data-test="tf-cli-accepted"]').exists()).toBe(true)
  })

  it('shows a blocking visual warning but permits the unverified compatibility path', async () => {
    protocol.findTfCli.mockResolvedValue({ port: 43114, verified: false })
    protocol.importKeyToTf.mockResolvedValue({ ok: false, status: 409, error: 'rejected' })
    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.find('[data-test="tf-cli-unverified"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="tf-cli-send"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-test="tf-cli-send"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="tf-cli-import-error"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('keys.tfImport.errors.rejected')
  })

  it('omits misleading single-group metadata for a composite key', async () => {
    protocol.findTfCli.mockResolvedValue({ port: 43111, verified: true })
    protocol.importKeyToTf.mockResolvedValue({ ok: true, status: 202 })
    const composite = {
      ...apiKey,
      id: 8,
      is_composite: true,
      composite_groups: [
        { group_id: 12, prefix: 'GPT' },
        { group_id: 13, prefix: 'CLAUDE' },
      ],
    } as ApiKey
    const wrapper = mountDialog(composite)
    await flushPromises()

    await wrapper.get('[data-test="tf-cli-send"]').trigger('click')
    await flushPromises()

    expect(protocol.importKeyToTf.mock.calls[0]?.[1]).toEqual({
      key: 'sk-secret',
      host: window.location.origin,
      key_name: 'Laptop',
    })
  })

  it('offers retry when no local tf service is found', async () => {
    protocol.findTfCli.mockResolvedValueOnce(null).mockResolvedValueOnce({
      port: 43112,
      verified: false,
    })
    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.find('[data-test="tf-cli-not-found"]').exists()).toBe(true)
    await wrapper.get('[data-test="tf-cli-retry"]').trigger('click')
    await flushPromises()

    expect(protocol.findTfCli).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="tf-cli-unverified"]').exists()).toBe(true)
  })

  it('aborts the browser wait when dismissed during terminal confirmation', async () => {
    let finishImport: ((value: { ok: true; status: 202 }) => void) | undefined
    protocol.findTfCli.mockResolvedValue({ port: 43110, verified: true })
    protocol.importKeyToTf.mockReturnValue(new Promise((resolve) => { finishImport = resolve }))
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get('[data-test="tf-cli-send"]').trigger('click')
    const requestSignal = protocol.importKeyToTf.mock.calls[0]?.[2] as AbortSignal
    await wrapper.get('[data-test="base-close"]').trigger('click')

    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(requestSignal.aborted).toBe(true)
    finishImport?.({ ok: true, status: 202 })
    await flushPromises()
  })
})
