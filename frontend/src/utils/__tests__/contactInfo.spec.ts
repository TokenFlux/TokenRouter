import { describe, expect, it } from 'vitest'
import { formatContactUrl, parseContactInfo } from '../contactInfo'

// contactInfo.spec.ts checks how the free-form contact setting becomes labeled entries
// shared by the header menu and the dashboard contact card. Inputs are literal setting
// strings, so the tests cover separators, label colons, and link classification directly.

describe('parseContactInfo', () => {
  // Blank settings must produce no entries. Both consumers hide themselves on an empty
  // list, so whitespace-only or missing text cannot render an empty menu or card.
  it('returns no entries for missing or blank text', () => {
    expect(parseContactInfo(undefined)).toEqual([])
    expect(parseContactInfo('  \n ')).toEqual([])
  })

  // Mixed ASCII and full-width separators and colons split one setting into labeled
  // entries. The test protects label extraction and the copy-only classification of
  // values that are not HTTP links.
  it('splits entries and labels on ASCII and full-width punctuation', () => {
    expect(parseContactInfo('QQ: 123456；微信：tokenflux\nsupport@example.com')).toEqual([
      { label: 'QQ', value: '123456', url: '' },
      { label: '微信', value: 'tokenflux', url: '' },
      { label: '', value: 'support@example.com', url: '' },
    ])
  })

  // A scheme colon must not be read as a label separator, while a labeled link still
  // splits at its first other colon. Both forms must keep the full URL as the link target.
  it('keeps scheme-prefixed links intact and marks them as URLs', () => {
    expect(parseContactInfo('https://t.me/tokenflux, Discord: https://discord.gg/abc')).toEqual([
      { label: '', value: 'https://t.me/tokenflux', url: 'https://t.me/tokenflux' },
      { label: 'Discord', value: 'https://discord.gg/abc', url: 'https://discord.gg/abc' },
    ])
  })

  // An entry whose label has no value after the colon is dropped. This keeps half-written
  // settings from producing rows that would copy an empty string.
  it('drops entries without a value', () => {
    expect(parseContactInfo('QQ:, Email: a@b.c')).toEqual([
      { label: 'Email', value: 'a@b.c', url: '' },
    ])
  })
})

describe('formatContactUrl', () => {
  // Display text drops the scheme and one trailing slash. The original URL stays the link
  // target, so only the visible label becomes shorter.
  it('shortens the visible link text', () => {
    expect(formatContactUrl('https://t.me/tokenflux/')).toBe('t.me/tokenflux')
    expect(formatContactUrl('http://example.com/path')).toBe('example.com/path')
  })
})
