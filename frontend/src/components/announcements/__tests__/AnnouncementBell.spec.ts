import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import type { UserAnnouncement } from '@/api/announcements.types'
import { useAnnouncementStore } from '@/stores/announcements'
import { nextMotionFrame } from '@/__tests__/helpers/motion'
import AnnouncementBell from '../AnnouncementBell.vue'

// AnnouncementBell.spec.ts checks the header announcement dialog through the mounted
// component: list rows, the detail layer that replaces the list inside the same
// dialog, and acknowledgement behavior. Local store state distinguishes unread and
// read entries, and DOM assertions cover row content, loading, empty content,
// keyboard dismissal order, and acknowledgement call counts.

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/utils/format', () => ({
  formatDate: () => 'August 1',
  formatDateTimeToMinute: () => '2026/08/01 12:00',
  formatRelativeTime: () => '3 hours ago',
}))

// createAnnouncement creates a user announcement fixture with deterministic
// identity and timestamps. Overrides replace individual fields so each test can
// select unread, acknowledged, or other store-visible states.
function createAnnouncement(
  id: number,
  overrides: Partial<UserAnnouncement> = {},
): UserAnnouncement {
  return {
    id,
    title: `Announcement ${id}`,
    content: `Content ${id}`,
    notify_mode: 'silent',
    created_at: `2026-08-01T${String(id).padStart(2, '0')}:00:00Z`,
    updated_at: `2026-08-01T${String(id).padStart(2, '0')}:00:00Z`,
    ...overrides,
  }
}

// openAnnouncementList mounts the bell in the document and opens its list
// dialog. It waits for Vue rendering before returning the wrapper so callers can
// inspect teleported list contents.
async function openAnnouncementList() {
  const wrapper = mount(AnnouncementBell, { attachTo: document.body })
  await wrapper.find('button').trigger('click')
  await nextTick()
  return wrapper
}

// listItems returns the rendered list rows in document order. Rows are teleported to
// the body, so the lookup starts there instead of at the mounted wrapper.
function listItems() {
  return [
    ...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="announcement-list-item"]'),
  ]
}

// findByTestId returns the first teleported element carrying a test identifier, or
// null when the dialog does not currently render it.
function findByTestId(testId: string) {
  return document.body.querySelector<HTMLElement>(`[data-testid="${testId}"]`)
}

// pressEscape dispatches the document-level Escape key that the bell listens for,
// then waits for the resulting render.
async function pressEscape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  await nextTick()
}

describe('AnnouncementBell', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    document.body.innerHTML = ''
    document.body.style.overflow = ''
    vi.restoreAllMocks()
  })

  // The header text variant shows the translated label from md upward and the bell
  // below md. An unread announcement adds only the red dot; the label keeps the shared
  // navigation color instead of switching to the unread accent.
  it('renders the header text trigger with an unread dot', () => {
    const store = useAnnouncementStore()
    store.announcements = [createAnnouncement(1)]

    const wrapper = mount(AnnouncementBell, { props: { variant: 'text' } })
    const trigger = wrapper.get('button')
    expect(trigger.classes()).toContain('header-nav-link')
    expect(trigger.classes()).not.toContain('text-primary-600')
    expect(trigger.get('span.md\\:inline').text()).toBe('announcements.title')
    expect(trigger.get('svg').classes()).toContain('md:hidden')
    expect(trigger.find('[data-testid="announcement-unread-dot"]').exists()).toBe(true)
  })

  // The list contains one unread and one acknowledged announcement. The dialog must
  // use the shared modal panel, mark only the unread row with a dot, and report an
  // unread count of one.
  it('列表弹窗复用共享弹窗外壳并用圆点标出未读公告', async () => {
    const store = useAnnouncementStore()
    store.announcements = [
      createAnnouncement(1),
      createAnnouncement(2, { read_at: '2026-08-01T13:00:00Z' }),
    ]

    const wrapper = await openAnnouncementList()
    const dialog = findByTestId('announcement-list-dialog')
    const items = listItems()

    expect(dialog?.classList).toContain('modal-content')
    expect(dialog?.classList).toContain('max-w-[640px]')
    expect(items).toHaveLength(2)
    expect(items[0].dataset.unread).toBe('true')
    expect(items[1].dataset.unread).toBe('false')
    expect(items[0].querySelector('[data-testid="announcement-list-unread-marker"]')).not.toBeNull()
    expect(items[1].querySelector('[data-testid="announcement-list-unread-marker"]')).toBeNull()
    expect(dialog?.textContent).toContain('1 announcements.unread')

    wrapper.unmount()
  })

  // One announcement opens with prose and another holds only a table. The prose row
  // must show its opening paragraph as a summary, and the table row must omit the
  // summary line.
  it('shows a one-line summary only when the content has prose', async () => {
    const store = useAnnouncementStore()
    store.announcements = [
      createAnnouncement(1, { content: 'Maintenance starts tonight.\n\nSecond paragraph.' }),
      createAnnouncement(2, { content: '| Size | Price |\n| --- | --- |\n| 1K | 2 credits |' }),
    ]

    const wrapper = await openAnnouncementList()
    const items = listItems()

    expect(items[0].querySelector('[data-testid="announcement-list-summary"]')?.textContent).toBe(
      'Maintenance starts tonight.',
    )
    expect(items[1].querySelector('[data-testid="announcement-list-summary"]')).toBeNull()

    wrapper.unmount()
  })

  // Announcements were published an hour ago, a month ago, and at a future time. Only
  // the recent row may show elapsed time; the others must show a calendar date because
  // the relative formatter labels future timestamps as never.
  it('shows elapsed time for recent announcements and a date for the rest', async () => {
    const hour = 60 * 60 * 1000
    const store = useAnnouncementStore()
    store.announcements = [
      createAnnouncement(1, { created_at: new Date(Date.now() - hour).toISOString() }),
      createAnnouncement(2, { created_at: new Date(Date.now() - 30 * 24 * hour).toISOString() }),
      createAnnouncement(3, { created_at: new Date(Date.now() + hour).toISOString() }),
    ]

    const wrapper = await openAnnouncementList()
    const dates = listItems().map((item) => item.querySelector('time')?.textContent?.trim())

    expect(dates).toEqual(['3 hours ago', 'August 1', 'August 1'])

    wrapper.unmount()
  })

  // The user opens a row and then returns. The announcement must appear inside the one
  // existing dialog with the list inert and focus on the back control, and returning
  // must restore the list and focus the row that was opened.
  it('opens the selected announcement inside the list dialog and returns to the list', async () => {
    const store = useAnnouncementStore()
    store.announcements = [createAnnouncement(1, { read_at: '2026-08-01T13:00:00Z' })]
    const wrapper = await openAnnouncementList()
    const listView = findByTestId('announcement-list-view')

    listItems()[0].click()
    await nextTick()
    await nextTick()

    const detail = findByTestId('announcement-detail-view')
    const back = findByTestId('announcement-detail-back')
    expect(document.body.querySelectorAll('[role="dialog"]')).toHaveLength(1)
    expect(detail?.querySelector('h2')?.textContent?.trim()).toBe('Announcement 1')
    expect(detail?.querySelector('.markdown-body')?.textContent).toContain('Content 1')
    expect(listView?.hasAttribute('inert')).toBe(true)
    expect(document.activeElement).toBe(back)

    back?.click()
    await nextTick()
    await nextTick()

    expect(listView?.hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(listItems()[0])

    wrapper.unmount()
  })

  // The detail layer is open when Escape is pressed twice. The first press must return
  // to the list and keep the dialog open, and the second press must close the dialog.
  it('Escape returns to the list before closing the dialog', async () => {
    const store = useAnnouncementStore()
    store.announcements = [createAnnouncement(1, { read_at: '2026-08-01T13:00:00Z' })]
    const wrapper = await openAnnouncementList()
    const listView = findByTestId('announcement-list-view')

    listItems()[0].click()
    await nextTick()
    await pressEscape()

    expect(listView?.hasAttribute('inert')).toBe(false)
    expect(findByTestId('announcement-list-dialog')).not.toBeNull()

    await pressEscape()
    await nextMotionFrame()

    expect(findByTestId('announcement-list-dialog')).toBeNull()

    wrapper.unmount()
  })

  // The dialog is closed from the detail layer and opened again. The selection must be
  // cleared once the exit finishes, so the reopened dialog starts at the list.
  it('reopens at the list after closing from the detail layer', async () => {
    const store = useAnnouncementStore()
    store.announcements = [createAnnouncement(1, { read_at: '2026-08-01T13:00:00Z' })]
    const wrapper = await openAnnouncementList()

    listItems()[0].click()
    await nextTick()
    findByTestId('announcement-detail-close')?.click()
    await nextMotionFrame()

    expect(findByTestId('announcement-list-dialog')).toBeNull()

    await wrapper.find('button').trigger('click')
    await nextTick()

    expect(findByTestId('announcement-list-dialog')).not.toBeNull()
    expect(findByTestId('announcement-detail-view')).toBeNull()

    wrapper.unmount()
  })

  // The user opens unread and previously read announcements in sequence, returning to
  // the list in between. The store must receive exactly one acknowledgement request,
  // for the announcement that was unread.
  it('点击未读公告时标记已读，已读公告不会重复提交', async () => {
    const store = useAnnouncementStore()
    store.announcements = [
      createAnnouncement(1),
      createAnnouncement(2, { read_at: '2026-08-01T13:00:00Z' }),
    ]
    const markAsRead = vi.spyOn(store, 'markAsRead').mockResolvedValue()
    const wrapper = await openAnnouncementList()

    listItems()[0].click()
    await flushPromises()
    findByTestId('announcement-detail-back')?.click()
    await flushPromises()
    listItems()[1].click()
    await flushPromises()

    expect(markAsRead).toHaveBeenCalledTimes(1)
    expect(markAsRead).toHaveBeenCalledWith(1)

    wrapper.unmount()
  })

  // The announcement store transitions from loading to an empty list. The dialog
  // must replace its loading placeholder with the translated empty-state title
  // and description.
  it('以紧凑样式展示加载和空状态', async () => {
    const store = useAnnouncementStore()
    store.loading = true
    const wrapper = await openAnnouncementList()

    expect(findByTestId('announcement-list-loading')).not.toBeNull()

    store.loading = false
    await nextTick()

    expect(findByTestId('announcement-list-loading')).toBeNull()
    expect(document.body.textContent).toContain('announcements.empty')
    expect(document.body.textContent).toContain('announcements.emptyDescription')

    wrapper.unmount()
  })

  // The store reports loading again after rows have arrived, as it does while marking
  // everything read. The rows must stay in place and no placeholder may replace them,
  // which would also discard the list scroll position.
  it('keeps loaded rows visible while the store is loading again', async () => {
    const store = useAnnouncementStore()
    store.announcements = [createAnnouncement(1)]
    const wrapper = await openAnnouncementList()

    store.loading = true
    await nextTick()

    expect(listItems()).toHaveLength(1)
    expect(findByTestId('announcement-list-loading')).toBeNull()

    wrapper.unmount()
  })
})
