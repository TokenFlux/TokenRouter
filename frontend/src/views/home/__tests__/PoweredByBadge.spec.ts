import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import PoweredByBadge from '../PoweredByBadge.vue'

// PoweredByBadge.spec.ts checks the homepage footer credit against the real
// English and Chinese catalogs. Cases cover the repository link target and the
// localized sentence, where the slot keeps the product name bold and untranslated.

// mountBadge mounts the footer credit with the shipped catalogs and selects the
// requested locale, so assertions read the same sentences visitors see.
function mountBadge(locale: 'en' | 'zh') {
  const i18n = createI18n({
    legacy: false,
    locale,
    messages: { en, zh },
  })
  return mount(PoweredByBadge, { global: { plugins: [i18n] } })
}

describe('PoweredByBadge', () => {
  // A visitor follows the footer credit from the public homepage. The link must
  // open the TokenRouter repository in a new tab without exposing window.opener.
  it('links to the TokenRouter repository in a new tab', () => {
    const link = mountBadge('en').get('a')

    expect(link.attributes('href')).toBe('https://github.com/TokenFlux/TokenRouter')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
  })

  // English and Chinese place the product name at different positions in the
  // sentence. Each locale must render its own wording around the same bold name.
  it.each([
    ['en', 'Powered by TokenRouter'],
    ['zh', '基于 TokenRouter 构建'],
  ] as const)('renders the %s credit with a bold product name', (locale, text) => {
    const wrapper = mountBadge(locale)

    expect(wrapper.text()).toBe(text)
    expect(wrapper.get('.font-semibold').text()).toBe('TokenRouter')
  })
})
