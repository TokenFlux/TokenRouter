import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { marked } from 'marked'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { finishMotion, mockMotionEnvironment } from '@/__tests__/helpers/motion'
import type { ReleaseInfo } from '@/api/admin/system'
import VersionBadge from '../VersionBadge.vue'

// VersionBadge.changelog.spec.ts verifies the in-app release-notes modal across
// source and release builds. Local store state and system API spies ensure that
// opening, inspecting, expanding, and closing the changelog reuse cached
// release metadata without triggering network fetches or maintenance actions.

// systemSpies records update, restart, rollback, and store fetch invocations so
// tests can confirm that changelog inspection leaves maintenance actions idle.
const systemSpies = vi.hoisted(() => ({
  fetchVersion: vi.fn(), // Cached version check spy.
  performUpdate: vi.fn(), // Binary update request spy.
  restartService: vi.fn(), // Service restart request spy.
  rollback: vi.fn(), // Binary rollback request spy.
  getRollbackVersions: vi.fn(), // Historical version list request spy.
}))

// storeState holds the reactive-like store fields read by VersionBadge during
// each test scenario.
const storeState = vi.hoisted(() => ({
  isAdmin: true, // Administrator role flag.
  versionLoading: false, // Store version loading state.
  currentVersion: '1.2.3', // Installed gateway version.
  latestVersion: '1.3.0', // Target release version.
  hasUpdate: true, // Update availability flag.
  buildType: 'release', // Active build type ("release" or "source").
  releaseInfo: null as ReleaseInfo | null, // Cached GitHub Release metadata.
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) =>
      params?.version ? `${key}:${params.version}` : key,
  }),
}))

vi.mock('@/i18n', () => ({
  getLocale: () => 'en-US',
}))

vi.mock('@/directives/segmented', () => ({
  vSegmented: {},
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    get isAdmin() {
      return storeState.isAdmin
    },
  }),
  useAppStore: () => ({
    get versionLoading() {
      return storeState.versionLoading
    },
    get currentVersion() {
      return storeState.currentVersion
    },
    get latestVersion() {
      return storeState.latestVersion
    },
    get hasUpdate() {
      return storeState.hasUpdate
    },
    get buildType() {
      return storeState.buildType
    },
    get releaseInfo() {
      return storeState.releaseInfo
    },
    fetchVersion: systemSpies.fetchVersion,
    clearVersionCache: vi.fn(),
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copied: false,
    copyToClipboard: vi.fn(),
  }),
}))

vi.mock('@/api/admin/system', () => ({
  performUpdate: systemSpies.performUpdate,
  restartService: systemSpies.restartService,
  rollback: systemSpies.rollback,
  getRollbackVersions: systemSpies.getRollbackVersions,
}))

// wrappers retains mounted component instances so afterEach cleanup unmounts
// teleported dialog shells and resets document focus and scroll locks.
const wrappers: VueWrapper[] = []

// bilingualMarkdown reproduces a GitHub Release body with English notes, fenced
// code blocks, and a collapsed Simplified Chinese details section.
const bilingualMarkdown = [
  '## New features',
  '',
  '- Added gateway release changelog dialog for administrators.',
  '',
  '```bash',
  'curl -sSL https://example.com/install.sh | bash',
  '```',
  '',
  '<details>',
  '<summary>中文翻译</summary>',
  '',
  '## 新增功能',
  '',
  '- 新增管理员站内网关更新日志弹窗。',
  '',
  '```text',
  '第一行配置',
  '第二行配置',
  '```',
  '',
  '</details>',
].join('\n')

beforeEach(() => {
  mockMotionEnvironment()
  storeState.isAdmin = true
  storeState.versionLoading = false
  storeState.currentVersion = '1.2.3'
  storeState.latestVersion = '1.3.0'
  storeState.hasUpdate = true
  storeState.buildType = 'release'
  storeState.releaseInfo = {
    name: 'Release 1.3.0',
    body: bilingualMarkdown,
    published_at: '2026-11-01T04:05:06Z',
    html_url: 'https://github.com/TokenFlux/TokenRouter/releases/tag/v1.3.0',
  }
  vi.clearAllMocks()
})

afterEach(() => {
  wrappers
    .splice(0)
    .reverse()
    .forEach((wrapper) => wrapper.unmount())
  document.body.classList.remove('modal-open')
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

// mountVersionBadge mounts VersionBadge attached to document.body with real
// transitions and icon stubs. Callers use the returned wrapper while afterEach
// unmounts it to clean up teleported DOM and shared dialog lifecycle state.
function mountVersionBadge() {
  const wrapper = mount(VersionBadge, {
    attachTo: document.body,
    global: {
      stubs: {
        transition: false,
        Icon: true,
      },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

describe('VersionBadge in-app changelog modal', () => {
  // Both source and release builds have an available update in cached store state.
  // Opening the changelog from either build must display the target version and
  // bilingual body without issuing another version request or system action.
  it.each(['release', 'source'] as const)(
    'opens the in-app changelog modal for %s builds using cached release data',
    async (buildType) => {
      storeState.buildType = buildType
      const wrapper = mountVersionBadge()
      expect(systemSpies.fetchVersion).toHaveBeenCalledTimes(1)
      expect(systemSpies.fetchVersion).toHaveBeenLastCalledWith(false)

      await wrapper.get('[data-testid="version-badge-trigger"]').trigger('click')
      const trigger = wrapper.get('[data-testid="version-view-changelog"]')
      await trigger.trigger('click')
      await flushPromises()

      const menu = document.body.querySelector<HTMLElement>('.dropdown')
      expect(menu?.hasAttribute('inert')).toBe(true)
      await finishMotion(menu!)
      expect(wrapper.find('.dropdown').exists()).toBe(false)
      expect(wrapper.findComponent(BaseDialog).props('trapFocus')).toBe(true)

      expect(systemSpies.fetchVersion).toHaveBeenCalledTimes(1)
      expect(systemSpies.performUpdate).not.toHaveBeenCalled()
      expect(systemSpies.restartService).not.toHaveBeenCalled()
      expect(systemSpies.rollback).not.toHaveBeenCalled()

      const dialog = document.body.querySelector<HTMLElement>(
        '[data-testid="version-changelog-modal"]',
      )
      expect(dialog).not.toBeNull()
      expect(dialog?.textContent).toContain('version.releaseNotes · v1.3.0')
      expect(
        dialog?.querySelector('[data-testid="changelog-target-version"]')?.textContent?.trim(),
      ).toBe('v1.3.0')
      expect(dialog?.textContent).toContain('Release 1.3.0')
      expect(dialog?.querySelector('[data-testid="changelog-published-at"]')).not.toBeNull()

      const githubLink = dialog?.querySelector<HTMLAnchorElement>(
        '[data-testid="changelog-github-link"]',
      )
      expect(githubLink?.getAttribute('href')).toBe(
        'https://github.com/TokenFlux/TokenRouter/releases/tag/v1.3.0',
      )
      expect(githubLink?.getAttribute('target')).toBe('_blank')
      expect(githubLink?.getAttribute('rel')).toBe('noopener noreferrer')

      const markdownBody = dialog?.querySelector<HTMLElement>(
        '[data-testid="changelog-markdown-body"]',
      )
      expect(markdownBody).not.toBeNull()
      expect(markdownBody?.textContent).toContain(
        'Added gateway release changelog dialog for administrators.',
      )
      expect(markdownBody?.querySelector('pre code')?.textContent).toContain(
        'curl -sSL https://example.com/install.sh | bash',
      )

      const details = markdownBody?.querySelector<HTMLDetailsElement>('details')
      const summary = details?.querySelector<HTMLElement>('summary')
      expect(details).not.toBeNull()
      expect(details?.hasAttribute('open')).toBe(false)
      expect(summary?.textContent).toBe('中文翻译')
      expect(details?.querySelector('h2')?.textContent).toBe('新增功能')
      expect(details?.textContent).toContain('新增管理员站内网关更新日志弹窗。')

      // Toggling the collapsible Chinese section reveals its content without side effects.
      details!.open = true
      details!.dispatchEvent(new Event('toggle'))
      await nextTick()
      expect(details?.open).toBe(true)
      expect(systemSpies.performUpdate).not.toHaveBeenCalled()
    },
  )

  // Multi-delimiter backtick and tilde fences, block-quoted fences, and four-space
  // indented blocks contain <details>, <summary>, and embedded shorter fences.
  // Rendering must preserve exact literal code text matching direct CommonMark parsing.
  it.each([
    [
      'four-backtick fence with embedded triple-backtick snippet',
      [
        '````markdown',
        '```html',
        '<details>',
        '<summary>Example</summary>',
        'literal',
        '</details>',
        '```',
        '````',
      ].join('\n'),
      ['```html', '<details>', '<summary>Example</summary>', 'literal', '</details>', '```'].join(
        '\n',
      ) + '\n',
    ],
    [
      'four-tilde fence with embedded triple-tilde snippet',
      [
        '~~~~markdown',
        '~~~html',
        '<details>',
        '<summary>Example</summary>',
        'literal',
        '</details>',
        '~~~',
        '~~~~',
      ].join('\n'),
      ['~~~html', '<details>', '<summary>Example</summary>', 'literal', '</details>', '~~~'].join(
        '\n',
      ) + '\n',
    ],
    [
      'quoted fenced code block',
      [
        '> ```html',
        '> <details>',
        '> <summary>Example</summary>',
        '> literal',
        '> </details>',
        '> ```',
      ].join('\n'),
      ['<details>', '<summary>Example</summary>', 'literal', '</details>'].join('\n') + '\n',
    ],
    [
      'four-space indented code block',
      ['    <details>', '    <summary>Example</summary>', '    literal', '    </details>'].join(
        '\n',
      ),
      ['<details>', '<summary>Example</summary>', 'literal', '</details>'].join('\n') + '\n',
    ],
  ])(
    'preserves exact literal code content inside %s',
    async (_, markdownInput, expectedLiteral) => {
      storeState.releaseInfo = {
        name: 'v1.4.1',
        body: markdownInput,
        published_at: '2026-11-02T10:00:00Z',
        html_url: 'https://github.com/TokenFlux/TokenRouter/releases/tag/v1.4.1',
      }

      const expectedContainer = document.createElement('div')
      expectedContainer.innerHTML = marked.parse(markdownInput, {
        async: false,
        gfm: true,
        breaks: true,
      }) as string
      const expectedCodeText = expectedContainer.querySelector('pre code')?.textContent

      const wrapper = mountVersionBadge()
      await wrapper.get('[data-testid="version-badge-trigger"]').trigger('click')
      await wrapper.get('[data-testid="version-view-changelog"]').trigger('click')
      await nextTick()

      const actualCodeText = document.body.querySelector(
        '[data-testid="changelog-markdown-body"] pre code',
      )?.textContent
      expect(actualCodeText).toBe(expectedCodeText)
      expect(actualCodeText).toBe(expectedLiteral)
    },
  )

  // Cached release metadata has an empty or missing Markdown body and invalid link/date.
  // The modal must display the empty state and omit invalid dates and unsafe links.
  it.each([
    ['empty body string', '   '],
    ['missing releaseInfo record', null],
  ] as const)(
    'renders a clear empty state and hides invalid date or link for %s',
    async (_, bodyValue) => {
      storeState.releaseInfo =
        bodyValue === null
          ? null
          : {
              name: 'v1.3.0',
              body: bodyValue,
              published_at: 'not-a-valid-date',
              html_url: 'javascript:alert(1)',
            }

      const wrapper = mountVersionBadge()
      await wrapper.get('[data-testid="version-badge-trigger"]').trigger('click')
      await wrapper.get('[data-testid="version-view-changelog"]').trigger('click')
      await nextTick()

      const dialog = document.body.querySelector<HTMLElement>(
        '[data-testid="version-changelog-modal"]',
      )
      expect(dialog).not.toBeNull()
      expect(dialog?.querySelector('[data-testid="changelog-empty-state"]')).not.toBeNull()
      expect(dialog?.querySelector('[data-testid="changelog-markdown-body"]')).toBeNull()
      expect(dialog?.textContent).toContain('version.noReleaseNotes')
      expect(dialog?.textContent).toContain('version.noReleaseNotesDesc')
      expect(dialog?.querySelector('[data-testid="changelog-published-at"]')).toBeNull()
      expect(dialog?.querySelector('[data-testid="changelog-github-link"]')).toBeNull()
    },
  )

  // Release notes contain scripts, dangerous attributes, and unsafe URL schemes.
  // Sanitization must remove executable markup and unsafe hrefs while keeping safe links.
  it('sanitizes scripts, unsafe attributes, and dangerous URLs in release notes', async () => {
    storeState.releaseInfo = {
      name: 'Security Test Release',
      body: [
        '## Security check',
        '',
        '<script>window.__changelogXss = true</script>',
        '<iframe src="https://evil.example/frame"></iframe>',
        '<img src="x" onerror="window.__imgXss = true">',
        '',
        '[Safe docs](https://github.com/TokenFlux/TokenRouter)',
        '[Script link](javascript:alert(1))',
        '[Data link](data:text/html,<script>alert(2)</script>)',
        '',
        '<a href="vbscript:msgbox(1)" onclick="alert(3)">Legacy link</a>',
        '<details ontoggle="alert(4)">',
        '<summary onclick="alert(5)">中文翻译</summary>',
        '',
        '- 安全清理后的中文内容',
        '',
        '</details>',
      ].join('\n'),
      published_at: '2026-11-03T00:00:00Z',
      html_url: '#',
    }

    const wrapper = mountVersionBadge()
    await wrapper.get('[data-testid="version-badge-trigger"]').trigger('click')
    await wrapper.get('[data-testid="version-view-changelog"]').trigger('click')
    await nextTick()

    const markdownBody = document.body.querySelector<HTMLElement>(
      '[data-testid="changelog-markdown-body"]',
    )
    expect(markdownBody).not.toBeNull()
    expect(markdownBody?.querySelector('script')).toBeNull()
    expect(markdownBody?.querySelector('iframe')).toBeNull()
    expect(markdownBody?.querySelector('img')).toBeNull()
    expect(markdownBody?.querySelector('[onerror], [onclick], [ontoggle]')).toBeNull()

    const linksWithHref = Array.from(markdownBody!.querySelectorAll<HTMLAnchorElement>('a[href]'))
    expect(linksWithHref).toHaveLength(1)
    expect(linksWithHref[0]?.getAttribute('href')).toBe('https://github.com/TokenFlux/TokenRouter')
    expect(linksWithHref[0]?.getAttribute('target')).toBe('_blank')
    expect(linksWithHref[0]?.getAttribute('rel')).toBe('noopener noreferrer')
    expect(markdownBody?.innerHTML).not.toContain('javascript:')
    expect(markdownBody?.innerHTML).not.toContain('vbscript:')
    expect(markdownBody?.querySelector('details')?.textContent).toContain('安全清理后的中文内容')
    expect(document.body.querySelector('[data-testid="changelog-github-link"]')).toBeNull()
  })

  // An administrator opens the changelog modal and closes it with Escape, the
  // footer button, and a backdrop click. Each close must dismiss the modal and
  // return focus to the persistent version badge after removing maintenance controls.
  it('supports Escape, button, and backdrop dismissal while restoring focus to the badge', async () => {
    const wrapper = mountVersionBadge()
    const badge = wrapper.get('[data-testid="version-badge-trigger"]')
    await badge.trigger('click')
    const trigger = wrapper.get('[data-testid="version-view-changelog"]')
    ;(trigger.element as HTMLElement).focus()

    await trigger.trigger('click')
    await flushPromises()
    const firstOverlay = document.body.querySelector<HTMLElement>('.modal-overlay')
    expect(firstOverlay).not.toBeNull()

    // Dismiss with Escape key and verify focus returns to the trigger button after leave.
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await finishMotion(firstOverlay!)
    expect(document.body.querySelector('[data-testid="version-changelog-modal"]')).toBeNull()
    expect(document.activeElement).toBe(badge.element)

    // Reopen and dismiss via the footer close button.
    await badge.trigger('click')
    await wrapper.get('[data-testid="version-view-changelog"]').trigger('click')
    await flushPromises()
    const secondOverlay = document.body.querySelector<HTMLElement>('.modal-overlay')
    const closeButton = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="changelog-modal-close"]',
    )
    expect(secondOverlay).not.toBeNull()
    expect(closeButton).not.toBeNull()
    closeButton!.click()
    await finishMotion(secondOverlay!)
    expect(document.body.querySelector('[data-testid="version-changelog-modal"]')).toBeNull()
    expect(document.activeElement).toBe(badge.element)

    // Reopen from the badge and dismiss via the modal backdrop.
    await badge.trigger('click')
    await wrapper.get('[data-testid="version-view-changelog"]').trigger('click')
    await flushPromises()
    const thirdOverlay = document.body.querySelector<HTMLElement>('.modal-overlay')
    expect(thirdOverlay).not.toBeNull()
    thirdOverlay!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await finishMotion(thirdOverlay!)
    expect(document.body.querySelector('[data-testid="version-changelog-modal"]')).toBeNull()
    expect(document.activeElement).toBe(badge.element)
  })

  // While the changelog modal is leaving, focus moves to another control or a newer
  // dialog opens. Completing the changelog leave transition must not steal focus
  // from the newer dialog or external control.
  it('does not steal focus from a newer dialog or external control during exit', async () => {
    const externalButton = document.createElement('button')
    externalButton.type = 'button'
    externalButton.textContent = 'External control'
    document.body.appendChild(externalButton)

    const wrapper = mountVersionBadge()
    await wrapper.get('[data-testid="version-badge-trigger"]').trigger('click')
    const trigger = wrapper.get('[data-testid="version-view-changelog"]')
    await trigger.trigger('click')
    await flushPromises()

    const firstOverlay = document.body.querySelector<HTMLElement>('.modal-overlay')!
    const closeButton = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="changelog-modal-close"]',
    )!
    closeButton.click()
    await nextTick()

    // Focus moves to an external control before the modal leave transition completes.
    externalButton.focus()
    await finishMotion(firstOverlay)
    expect(document.activeElement).toBe(externalButton)

    // Reopen the changelog, begin closing it, and open a newer dialog before exit finishes.
    await wrapper.get('[data-testid="version-badge-trigger"]').trigger('click')
    await wrapper.get('[data-testid="version-view-changelog"]').trigger('click')
    await flushPromises()
    const secondOverlay = document.body.querySelector<HTMLElement>('.modal-overlay')!
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()

    const newerDialog = mount(BaseDialog, {
      attachTo: document.body,
      props: {
        show: true,
        title: 'Newer dialog',
        panelData: { 'data-testid': 'newer-dialog' },
      },
      slots: { default: '<button type="button" data-testid="newer-dialog-btn">Confirm</button>' },
      global: { stubs: { transition: false, Icon: true } },
    })
    wrappers.push(newerDialog)
    await nextTick()

    const newerPanel = document.body.querySelector<HTMLElement>('[data-testid="newer-dialog"]')
    const newerInitialFocus = document.activeElement
    expect(newerPanel).not.toBeNull()
    expect(newerPanel!.contains(newerInitialFocus)).toBe(true)

    await finishMotion(secondOverlay)
    expect(document.activeElement).toBe(newerInitialFocus)
    expect(document.activeElement).not.toBe(
      wrapper.get('[data-testid="version-badge-trigger"]').element,
    )
    expect(document.body.classList.contains('modal-open')).toBe(true)
  })
})
