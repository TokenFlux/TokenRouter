import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { sanitizeUrl } from '@/utils/url'

const dir = dirname(fileURLToPath(import.meta.url))
const entrySources = [
  {
    name: 'AppHeader',
    source: readFileSync(resolve(dir, '../AppHeader.vue'), 'utf8')
  },
  {
    name: 'HomeView',
    source: readFileSync(resolve(dir, '../../../views/HomeView.vue'), 'utf8')
  },
  {
    name: 'KeyUsageView',
    source: readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')
  },
  {
    name: 'ModelMarketplaceView',
    source: readFileSync(resolve(dir, '../../../views/ModelMarketplaceView.vue'), 'utf8')
  }
]

describe('doc_url sanitization', () => {
  it.each(entrySources)('$name applies sanitizeUrl to its document link', ({ source }) => {
    expect(source).toContain("import { sanitizeUrl } from '@/utils/url'")
    expect(source).toMatch(/const docUrl = computed\(\(\) => sanitizeUrl\(/)
  })

  it('rejects executable and non-web protocols', () => {
    expect(sanitizeUrl('javascript:alert(1)')).toBe('')
    expect(sanitizeUrl('data:text/html,<script>alert(1)</script>')).toBe('')
    expect(sanitizeUrl('//evil.example/path')).toBe('')
  })

  it('keeps valid HTTP and HTTPS document links', () => {
    expect(sanitizeUrl('https://docs.example.com/guide')).toBe('https://docs.example.com/guide')
    expect(sanitizeUrl('http://docs.example.com')).toBe('http://docs.example.com/')
  })
})
