/**
 * download.ts provides shared browser Blob download helpers and filename-safe
 * export timestamps so provider, proxy, and certificate exports release object
 * URLs immediately after triggering a synthetic anchor click.
 */

/**
 * formatExportTimestamp returns a compact local calendar timestamp
 * (YYYYMMDDHHmmss) used in exported provider and proxy backup filenames.
 */
export function formatExportTimestamp(now: Date = new Date()): string {
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`
}

/**
 * downloadBlob triggers a browser file download for blob under filename and
 * revokes the temporary object URL immediately after clicking the hidden link.
 */
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  // Revoke the temporary Blob URL immediately after synthetic click dispatch so
  // repeated exports do not leak browser memory.
  URL.revokeObjectURL(url)
}

/**
 * downloadJsonBlob serializes data with two-space indentation into a JSON Blob
 * and downloads it under filename.
 */
export function downloadJsonBlob(data: unknown, filename: string): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  downloadBlob(blob, filename)
}
