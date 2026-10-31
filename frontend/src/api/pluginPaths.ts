// pluginPaths.ts constructs contribution URLs without permitting a caller to
// escape its installed plugin namespace. Classification also distinguishes raw
// plugin responses from native panel envelopes, keeping plugin-owned code/data
// properties and explicitly supplied query values intact in the shared client.

/** pluginApiPath validates a plugin-relative path and joins its fixed API namespace.
 * Query values belong in request options. Encoded traversal, authority syntax,
 * backslashes, and control characters fail before authenticated dispatch occurs.
 */
export function pluginApiPath(pluginId: string, path: string): string {
  if (pluginId.length > 80 || !/^[a-z][a-z0-9-]{1,31}\.[a-z][a-z0-9-]{1,47}$/.test(pluginId)) {
    throw new Error('Invalid plugin identity')
  }
  let decoded: string
  try {
    decoded = decodeURIComponent(path)
  } catch {
    throw new Error('Invalid plugin request path')
  }
  // eslint-disable-next-line no-control-regex -- Reject control bytes before URL normalization.
  const unsafeCharacters = /[\\?#\u0000-\u001f\u007f]/
  if (
    !path.startsWith('/') ||
    !decoded.startsWith('/') ||
    decoded.includes('//') ||
    unsafeCharacters.test(decoded) ||
    decoded.split('/').some((part) => part === '.' || part === '..')
  ) {
    throw new Error('Plugin request path must remain inside its API namespace')
  }
  return `/plugins/${encodeURIComponent(pluginId)}/api${path}`
}

/** isPluginApiPath identifies contribution requests made through the shared API client. */
export function isPluginApiPath(value: unknown): boolean {
  return /^\/(?:api\/v1\/)?plugins\/[^/]+\/api(?:\/|$)/.test(String(value || ''))
}
