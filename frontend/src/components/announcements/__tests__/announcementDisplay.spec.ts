import { describe, expect, it } from 'vitest'
import { marked } from 'marked'
import { renderAnnouncementHtml, summarizeAnnouncement } from '../announcementDisplay'

// announcementDisplay.spec.ts checks the announcement content conversions shared by
// the header list, the dashboard timeline, and the detail article. The cases call the
// module directly with Markdown input and compare the returned HTML or summary text,
// including when the shared parser defaults differ from the announcement options.

describe('announcementDisplay', () => {
  // The shared parser defaults ignore single newlines, as they do before any view
  // changes them, and the content carries a script tag. The rendered announcement
  // must still keep the line break and must not contain the script.
  it('keeps single line breaks and removes scripts whatever the shared parser defaults', () => {
    marked.setOptions({ breaks: false })

    const html = renderAnnouncementHtml('Line one\nLine two<script>window.__xss = true</script>')

    expect(html).toContain('Line one<br>Line two')
    expect(html).not.toContain('<script')
  })

  // Content opens with a heading and a table before its first paragraph, and another
  // announcement holds only a table. The summary must be the first prose paragraph,
  // and table-only content must produce an empty summary.
  it('summarizes the first prose paragraph and returns nothing for table-only content', () => {
    const table = '| Size | Price |\n| --- | --- |\n| 1K | 2 credits |'

    expect(summarizeAnnouncement(`## Pricing\n\n${table}\n\nPrices change tonight.`)).toBe(
      'Prices change tonight.',
    )
    expect(summarizeAnnouncement(table)).toBe('')
  })

  // The first paragraph spans two lines in English, in Chinese, and in Chinese ending
  // with a number. Words separated by the break must stay apart, while two Chinese
  // lines must join without the space that Chinese text does not use.
  it('joins the lines of a paragraph with a space except between CJK characters', () => {
    expect(summarizeAnnouncement('Prices change\ntonight.')).toBe('Prices change tonight.')
    expect(summarizeAnnouncement('价格今晚调整。\n请提前迁移。')).toBe('价格今晚调整。请提前迁移。')
    expect(summarizeAnnouncement('可能出现 429\n建议切换分组')).toBe('可能出现 429 建议切换分组')
  })
})
