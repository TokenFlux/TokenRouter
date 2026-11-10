import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import type { UserAnnouncement } from '@/api/announcements.types'
import { useAnnouncementStore } from '@/stores/announcements'
import AnnouncementBell from '../AnnouncementBell.vue'

// AnnouncementBell.spec.ts checks announcement list presentation and
// acknowledgement behavior through the mounted header component. Local store
// state distinguishes unread and read entries, and DOM assertions cover loading,
// empty content, dialog styling, and acknowledgement call counts.

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
  formatDateTime: () => '2026/08/01 12:00:00',
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

  // The list contains one unread and one acknowledged announcement. The dialog
  // must render its shared card styles, separate read indicators, and an unread
  // count of one.
  it('使用与公告详情一致的轻量卡片并区分已读状态', async () => {
    const store = useAnnouncementStore()
    store.announcements = [
      createAnnouncement(1),
      createAnnouncement(2, { read_at: '2026-08-01T13:00:00Z' }),
    ]

    const wrapper = await openAnnouncementList()
    const dialog = document.body.querySelector('[data-testid="announcement-list-dialog"]')
    const items = document.body.querySelectorAll<HTMLButtonElement>(
      '[data-testid="announcement-list-item"]',
    )

    expect(dialog?.classList).toContain('max-w-[640px]')
    expect(dialog?.classList).toContain('rounded-surface')
    expect(dialog?.classList).toContain('sm:rounded-dialog')
    expect(dialog?.classList).toContain('dark:bg-dark-900')
    expect(dialog?.querySelector('[class*="bg-gradient"]')).toBeNull()
    expect(items).toHaveLength(2)
    expect(items[0]).toBeInstanceOf(HTMLButtonElement)
    expect(items[0].dataset.unread).toBe('true')
    expect(items[1].dataset.unread).toBe('false')
    expect(
      dialog?.querySelectorAll('[data-testid="announcement-list-status-unread"]'),
    ).toHaveLength(1)
    expect(dialog?.querySelectorAll('[data-testid="announcement-list-status-read"]')).toHaveLength(
      1,
    )
    expect(
      dialog?.querySelectorAll('[data-testid="announcement-list-unread-marker"]'),
    ).toHaveLength(1)
    expect(dialog?.textContent).toContain('1 announcements.unread')

    wrapper.unmount()
  })

  // The user opens unread and previously read announcements in sequence. The
  // store must receive exactly one acknowledgement request, for the announcement
  // that was unread.
  it('点击未读公告时标记已读，已读公告不会重复提交', async () => {
    const store = useAnnouncementStore()
    store.announcements = [
      createAnnouncement(1),
      createAnnouncement(2, { read_at: '2026-08-01T13:00:00Z' }),
    ]
    const markAsRead = vi.spyOn(store, 'markAsRead').mockResolvedValue()
    const wrapper = await openAnnouncementList()
    const items = document.body.querySelectorAll<HTMLButtonElement>(
      '[data-testid="announcement-list-item"]',
    )

    items[0].click()
    await flushPromises()
    items[1].click()
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

    expect(document.body.querySelector('[data-testid="announcement-list-loading"]')).not.toBeNull()

    store.loading = false
    await nextTick()

    expect(document.body.querySelector('[data-testid="announcement-list-loading"]')).toBeNull()
    expect(document.body.textContent).toContain('announcements.empty')
    expect(document.body.textContent).toContain('announcements.emptyDescription')

    wrapper.unmount()
  })
})
