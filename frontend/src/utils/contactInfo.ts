/**
 * contactInfo.ts parses the administrator's free-form contact setting into
 * labeled entries for the header Contact Us menu and the dashboard contact
 * card. Commas, semicolons, and newlines separate entries, while a colon can
 * split a label from its value. HTTP and HTTPS values become external links and
 * other values remain copyable text.
 *
 * The parser skips a colon followed by two slashes when searching for a label
 * separator, allowing ordinary scheme-prefixed links to remain intact. A later
 * colon can still split the entry, including one in a port or URL path.
 */

/**
 * ContactEntry represents one parsed contact value and its optional label.
 */
export interface ContactEntry {
  label: string // Optional display label
  value: string // Trimmed contact value
  /**
   * Contains the value when it is a whitespace-free HTTP or HTTPS URL;
   * otherwise it is empty and consumers offer a copy action.
   */
  url: string
}

/**
 * parseContactInfo splits configured contact text into display entries.
 * Missing or blank text returns an empty list, and entries whose value is
 * empty after trimming are discarded.
 * Each surviving value is classified as an HTTP link or copyable text.
 */
export function parseContactInfo(raw: string | null | undefined): ContactEntry[] {
  const text = raw?.trim()
  if (!text) return []
  return text
    .split(/[，,;；\n]+/)
    .map((part) => part.trim())
    .filter(Boolean)
    .map(parseContactEntry)
    .filter((entry) => entry.value)
}

/**
 * parseContactEntry splits one trimmed entry at its label separator and
 * classifies the value as a link or copyable text.
 * Scheme colons followed by two slashes are skipped so a plain HTTP URL can
 * remain a complete value. The first other ASCII or full-width colon after
 * the beginning separates the label, including colons in a port or URL path.
 * Only whitespace-free HTTP and HTTPS values receive a link target; other
 * values remain available for copying.
 */
function parseContactEntry(part: string): ContactEntry {
  // Skip the scheme delimiter so a plain HTTP link can remain the value; other
  // colon occurrences follow the label-separator rule.
  let sep = -1
  for (let i = 0; i < part.length; i++) {
    const ch = part[i]
    if (ch === '：') {
      sep = i
      break
    }
    if (ch === ':' && part.slice(i, i + 3) !== '://') {
      sep = i
      break
    }
  }
  let label = ''
  let value = part
  if (sep > 0) {
    label = part.slice(0, sep).trim()
    value = part.slice(sep + 1).trim()
  }
  const url = /^https?:\/\/\S+$/.test(value) ? value : ''
  return { label, value, url }
}

/**
 * formatContactUrl removes the HTTP scheme and one trailing slash from display
 * text. Link targets keep the original parsed URL, so shortening the visible
 * label does not change the address opened by the browser.
 */
export function formatContactUrl(url: string): string {
  return url.replace(/^https?:\/\//, '').replace(/\/$/, '')
}
