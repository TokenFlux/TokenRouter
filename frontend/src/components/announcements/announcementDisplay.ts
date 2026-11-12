import { marked } from 'marked'
import DOMPurify from 'dompurify'
import type { Announcement, UserAnnouncement } from '@/api/announcements.types'
import { formatDate } from '@/utils/format'

// announcementDisplay.ts converts announcement Markdown and publication timestamps
// into the HTML, plain-text summaries, and calendar dates shown by announcement
// surfaces. The header list, the dashboard timeline, and the detail article all read
// the same administrator-authored content, so one parser configuration and one
// sanitizer pass live here. A surface that parsed content separately could keep line
// breaks or markup that the others drop.

// DisplayedAnnouncement names the fields an announcement detail renders. Administrator
// records, user records, and unsaved previews all satisfy it.
export type DisplayedAnnouncement = Pick<
  Announcement | UserAnnouncement,
  'title' | 'content' | 'created_at'
>

// MARKDOWN_OPTIONS enables GitHub-flavored Markdown and renders single newlines as line
// breaks. Every parse passes these options explicitly because other views change the
// defaults of the shared marked instance for their own content.
const MARKDOWN_OPTIONS = { async: false, breaks: true, gfm: true } as const

// CJK_CHARACTER matches Han characters, kana, and full-width punctuation. Text in
// these scripts runs on without spaces, which decides how a summary joins two lines.
const CJK_CHARACTER = /[\u2E80-\u9FFF\uFF00-\uFFEF]/

// renderAnnouncementHtml parses announcement Markdown, which may embed raw HTML, and
// returns sanitized markup for v-html insertion. Empty content returns an empty string
// without invoking the parser.
export function renderAnnouncementHtml(content: string): string {
  if (!content) return ''
  return DOMPurify.sanitize(marked.parse(content, MARKDOWN_OPTIONS))
}

// summarizeAnnouncement returns the first nonempty prose paragraph of an announcement
// as plain text with collapsed whitespace. Tables and code blocks are removed first
// because their flattened values lose the rows and formatting that make them readable.
// Content without a paragraph uses the remaining body text, and content holding only
// tables or code returns an empty string so callers can omit the summary or show a
// placeholder.
export function summarizeAnnouncement(content: string): string {
  if (!content) return ''

  const parsed = new DOMParser().parseFromString(renderAnnouncementHtml(content), 'text/html')
  parsed.body.querySelectorAll('table, pre').forEach((element) => element.replaceWith(' '))
  // A line break carries no text, so the words on either side would join into one.
  // Replace it with a space, except between two CJK characters, which need none.
  parsed.body.querySelectorAll('br').forEach((lineBreak) => {
    const before = lineBreak.previousSibling?.textContent?.trimEnd().slice(-1) ?? ''
    const after = lineBreak.nextSibling?.textContent?.trimStart().charAt(0) ?? ''
    lineBreak.replaceWith(CJK_CHARACTER.test(before) && CJK_CHARACTER.test(after) ? '' : ' ')
  })
  const paragraph = [...parsed.body.querySelectorAll('p')].find((element) =>
    element.textContent?.trim(),
  )
  return (paragraph?.textContent || parsed.body.textContent || '').replace(/\s+/g, ' ').trim()
}

// formatAnnouncementDate formats a publication date as a month and day in the current
// locale. Dates in another calendar year include the year, and invalid timestamps
// return an empty string.
export function formatAnnouncementDate(createdAt: string): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return ''

  const options: Intl.DateTimeFormatOptions = {
    month: 'long',
    day: 'numeric',
  }
  if (date.getFullYear() !== new Date().getFullYear()) {
    options.year = 'numeric'
  }
  return formatDate(date, options)
}
