// pluginErrors.ts projects panel failures into plugin administration feedback.
// Field metadata contains schema paths and constraint descriptions, never submitted
// values. Malformed metadata leaves the ordinary request message available while
// preventing unrelated fields from receiving misleading validation feedback.

/** pluginErrorMessage returns a server-provided message or the caller's localized fallback. */
export function pluginErrorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string')
    return error.message
  return fallback
}

/** pluginFieldErrors extracts recognized schema failures from bounded server metadata.
 * Only fields in the current manifest are retained. Unknown constraint wording
 * selects a generic localized failure; publisher values and raw metadata are never rendered.
 */
export function pluginFieldErrors(
  error: unknown,
  keys: readonly string[],
  translate: (key: string) => string,
): Record<string, string> {
  if (!error || typeof error !== 'object' || !('metadata' in error)) return {}
  const metadata = error.metadata
  if (
    !metadata ||
    typeof metadata !== 'object' ||
    !('fields' in metadata) ||
    typeof metadata.fields !== 'string'
  )
    return {}
  try {
    const issues: unknown = JSON.parse(metadata.fields)
    if (!Array.isArray(issues)) return {}
    return Object.fromEntries(
      issues.flatMap((issue) => {
        if (!issue || typeof issue.path !== 'string' || !keys.includes(issue.path)) return []
        const reason = issue.message === 'must match the declared pattern' ? 'pattern' : 'rejected'
        return [[issue.path, translate(`plugins.fields.errors.${reason}`)]]
      }),
    )
  } catch {
    // An intermediary may return malformed diagnostic metadata; keep the request's
    // ordinary error message instead of failing the editor's recovery path while parsing it.
    return {}
  }
}

/** PluginUpdateFailure retains bounded startup diagnostics and the observed recovery result. */
export interface PluginUpdateFailure {
  restoredVersion: string // Successfully resumed old release version.
  rollbackError: string // Failure while restoring the old release.
  startError: string // Rejected target generation startup diagnostic.
  logs: string[] // At most fifty retained diagnostic messages.
}

/** pluginUpdateFailure reads recovery metadata without treating remote diagnostic text as HTML.
 * Unknown or malformed fields become empty strings or an empty list. Only the
 * documented update-failure reason permits this projection, avoiding misleading recovery claims.
 */
export function pluginUpdateFailure(error: unknown): PluginUpdateFailure | null {
  if (
    !error ||
    typeof error !== 'object' ||
    !('reason' in error) ||
    error.reason !== 'PLUGIN_UPDATE_FAILED' ||
    !('metadata' in error) ||
    !error.metadata ||
    typeof error.metadata !== 'object'
  )
    return null
  const metadata = error.metadata as Record<string, unknown>
  const result: PluginUpdateFailure = {
    restoredVersion: typeof metadata.restored_version === 'string' ? metadata.restored_version : '',
    rollbackError: typeof metadata.rollback_error === 'string' ? metadata.rollback_error : '',
    startError: typeof metadata.start_error === 'string' ? metadata.start_error : '',
    logs: [],
  }
  try {
    const lines: unknown = typeof metadata.logs === 'string' ? JSON.parse(metadata.logs) : []
    if (Array.isArray(lines))
      result.logs = lines
        .slice(-50)
        .flatMap((line) => (line && typeof line.message === 'string' ? [line.message] : []))
  } catch {
    // Invalid optional log metadata cannot hide the startup or restoration result;
    // retain those independently validated strings and leave the diagnostic list empty.
  }
  return result
}
