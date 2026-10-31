import { describe, expect, it } from 'vitest'
import { isPluginApiPath, pluginApiPath } from '../pluginPaths'

// pluginPaths.spec.ts checks contribution namespace construction and native-client
// classification. Cases cover valid plugin identities, encoded traversal, authority
// escapes, and management URLs so opaque plugin payload handling cannot spread to
// unrelated native API responses.

describe('plugin contribution paths', () => {
  // A valid plugin requests an endpoint inside its assigned namespace. Encoding
  // stays unchanged while the fixed host prefix prevents cross-plugin selection.
  it('joins one validated plugin namespace', () => {
    expect(pluginApiPath('tokenflux.checkin', '/records/day-1')).toBe(
      '/plugins/tokenflux.checkin/api/records/day-1',
    )
    expect(pluginApiPath('tokenflux.checkin', '/')).toBe('/plugins/tokenflux.checkin/api/')
  })

  // Plugin callers supply authority syntax and traversal in several encodings.
  // Rejection happens before a shared authenticated client can send the request.
  it.each([
    'https://outside.invalid',
    '//outside.invalid',
    '%2Fstatus',
    '/../users',
    '/%2e%2e/users',
    '/a/../users',
    '/a\\b',
    '/a?token=x',
    '/a#fragment',
    '/%00',
    '/%ZZ',
  ])('rejects unsafe request path %s', (path) =>
    expect(() => pluginApiPath('tokenflux.checkin', path)).toThrow(),
  )

  // Only the backend's publisher-and-name identifier shape can select a namespace.
  // A third segment, path separator, short publisher, or identity exceeding 80 characters must not become an authenticated URL.
  it.each([
    'a.plugin',
    'test.a',
    'test.plugin.extra',
    'test/plugin',
    '../plugin',
    `${'a'.repeat(32)}.${'b'.repeat(48)}`,
  ])('rejects invalid identity %s', (id) => expect(() => pluginApiPath(id, '/')).toThrow())

  // Raw contribution URLs and native plugin administration share a noun but own
  // different response contracts. Classification selects only the API contribution branch.
  it('keeps native discovery and administration on envelope handling', () => {
    expect(isPluginApiPath('/plugins/test.plugin/api/action')).toBe(true)
    expect(isPluginApiPath('/api/v1/plugins/test.plugin/api/action')).toBe(true)
    expect(isPluginApiPath('/plugins/ui')).toBe(false)
    expect(isPluginApiPath('/admin/plugins/test.plugin/config')).toBe(false)
    expect(isPluginApiPath('/plugins/test.plugin/assets/1.0.0/frontend/index.js')).toBe(false)
  })
})
