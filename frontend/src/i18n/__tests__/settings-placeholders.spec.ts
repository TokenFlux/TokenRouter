import { describe, expect, it } from 'vitest'
// 项目运行时别名使用 vue-i18n runtime-only 版本；这里需要 full build 验证消息编译行为。
import { createI18n } from '../../../node_modules/vue-i18n/dist/vue-i18n.mjs'
import enMessages from '../locales/en'
import zhMessages from '../locales/zh'

describe('settings i18n placeholders', () => {
  it.each([
    ['zh', zhMessages],
    ['en', enMessages]
  ])('renders Claude OAuth blocks placeholder as literal JSON for %s', (locale, messages) => {
    const i18n = createI18n({
      legacy: false,
      locale,
      messages: {
        [locale]: messages
      }
    })

    const placeholder = i18n.global.t(
      'admin.settings.gatewayForwarding.claudeOAuthSystemPromptBlocksPlaceholder'
    )

    expect(() => JSON.parse(placeholder)).not.toThrow()
    expect(placeholder).toContain('"blocks"')
    expect(placeholder).toContain('{billing_header}')
  })
})
