import type { OpsSeverity } from '@/api/admin/ops'

/**
 * opsErrorFormatting.ts formats severity badge classes and compact local
 * timestamps shared across OpsErrorLogTable and Ops dashboard components so
 * shared error-log tables in src/components/ops do not depend on view-private
 * modules.
 */

/**
 * getSeverityClass returns the severity badge palette used by Ops tables,
 * mapping empty or unrecognized severity codes to the neutral P3 style.
 */
export function getSeverityClass(severity: OpsSeverity): string {
  const classes: Record<string, string> = {
    P0: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400',
    P1: 'bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-400',
    P2: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400',
    P3: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400',
  }
  return classes[String(severity || '')] || classes.P3
}

/**
 * formatDateTime renders a timestamp as local MM-DD HH:mm:ss, returning an
 * empty string when dateStr cannot be parsed.
 */
export function formatDateTime(dateStr: string): string {
  const d = new Date(dateStr)
  if (Number.isNaN(d.getTime())) return ''
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`
}
