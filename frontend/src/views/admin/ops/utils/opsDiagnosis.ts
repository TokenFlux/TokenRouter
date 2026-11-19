import type { OpsDashboardOverview, OpsMetricThresholds } from '@/api/admin/ops.types'

/**
 * opsDiagnosis.ts formats local datetime-local bounds without UTC timezone drift
 * and evaluates operations health diagnostics using operator-configured metric
 * thresholds with deterministic default fallbacks.
 */

/**
 * DiagnosisItem carries a translated severity, consequence, and optional action
 * for the OpsDashboardHeader health report.
 */
export interface DiagnosisItem {
  type: 'critical' | 'warning' | 'info' // Diagnostic severity display band.
  message: string // Translated diagnostic message text.
  impact: string // Translated diagnostic consequence text.
  action?: string // Optional translated recovery suggestion.
}

/**
 * OpsTranslateFn translates an i18n key with optional named parameters.
 */
export type OpsTranslateFn = (key: string, params?: Record<string, unknown>) => string

/**
 * formatDateTimeLocalInput formats a Date into local YYYY-MM-DDTHH:mm text
 * suitable for HTML datetime-local inputs without shifting by the browser's UTC
 * offset.
 */
export function formatDateTimeLocalInput(date: Date): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  const hh = String(date.getHours()).padStart(2, '0')
  const mm = String(date.getMinutes()).padStart(2, '0')
  return `${y}-${m}-${d}T${hh}:${mm}`
}

/**
 * parseDateTimeLocalInputToISO parses local YYYY-MM-DDTHH:mm text into an ISO
 * UTC timestamp, returning null when the input is blank or invalid.
 */
export function parseDateTimeLocalInputToISO(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed) return null
  const parsed = new Date(trimmed)
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString()
}

/**
 * buildOpsDiagnosisReport evaluates resource, TTFT, upstream error rate, request
 * error rate, SLA, and health-score observations in priority order. Configured
 * OpsMetricThresholds override hardcoded defaults so header metric badges and
 * the textual diagnosis report agree on warning and critical cutoffs.
 */
export function buildOpsDiagnosisReport(
  overview: OpsDashboardOverview | null,
  thresholds: OpsMetricThresholds | null | undefined,
  isSystemIdle: boolean,
  t: OpsTranslateFn,
): DiagnosisItem[] {
  if (!overview) return []

  const report: DiagnosisItem[] = []
  if (isSystemIdle) {
    report.push({
      type: 'info',
      message: t('admin.ops.diagnosis.idle'),
      impact: t('admin.ops.diagnosis.idleImpact'),
    })
    return report
  }

  const sm = overview.system_metrics
  if (sm) {
    if (sm.db_ok === false) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.dbDown'),
        impact: t('admin.ops.diagnosis.dbDownImpact'),
        action: t('admin.ops.diagnosis.dbDownAction'),
      })
    }
    if (sm.redis_ok === false) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.redisDown'),
        impact: t('admin.ops.diagnosis.redisDownImpact'),
        action: t('admin.ops.diagnosis.redisDownAction'),
      })
    }

    const cpuPct = sm.cpu_usage_percent ?? 0
    if (cpuPct > 90) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.cpuCritical', { usage: cpuPct.toFixed(1) }),
        impact: t('admin.ops.diagnosis.cpuCriticalImpact'),
        action: t('admin.ops.diagnosis.cpuCriticalAction'),
      })
    } else if (cpuPct > 80) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.cpuHigh', { usage: cpuPct.toFixed(1) }),
        impact: t('admin.ops.diagnosis.cpuHighImpact'),
        action: t('admin.ops.diagnosis.cpuHighAction'),
      })
    }

    const memPct = sm.memory_usage_percent ?? 0
    if (memPct > 90) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.memoryCritical', { usage: memPct.toFixed(1) }),
        impact: t('admin.ops.diagnosis.memoryCriticalImpact'),
        action: t('admin.ops.diagnosis.memoryCriticalAction'),
      })
    } else if (memPct > 85) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.memoryHigh', { usage: memPct.toFixed(1) }),
        impact: t('admin.ops.diagnosis.memoryHighImpact'),
        action: t('admin.ops.diagnosis.memoryHighAction'),
      })
    }

    const diskPct = sm.disk_usage_percent ?? 0
    if (diskPct > 95) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.diskCritical', { usage: diskPct.toFixed(1) }),
        impact: t('admin.ops.diagnosis.diskCriticalImpact'),
        action: t('admin.ops.diagnosis.diskCriticalAction'),
      })
    } else if (diskPct > 85) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.diskHigh', { usage: diskPct.toFixed(1) }),
        impact: t('admin.ops.diagnosis.diskHighImpact'),
        action: t('admin.ops.diagnosis.diskHighAction'),
      })
    }
  }

  // Prefer operator-configured TTFT P99 cutoff (warning at 80% of configured max)
  // and fall back to 500ms when unconfigured.
  const ttftP99 = overview.ttft?.p99_ms ?? 0
  const configuredTtftMax = thresholds?.ttft_p99_ms_max
  const ttftWarnCutoff = configuredTtftMax != null ? configuredTtftMax * 0.8 : 500
  if (configuredTtftMax != null ? ttftP99 >= ttftWarnCutoff : ttftP99 > ttftWarnCutoff) {
    report.push({
      type: configuredTtftMax != null && ttftP99 >= configuredTtftMax ? 'critical' : 'warning',
      message: t('admin.ops.diagnosis.ttftHigh', { ttft: ttftP99.toFixed(0) }),
      impact: t('admin.ops.diagnosis.ttftHighImpact'),
      action: t('admin.ops.diagnosis.ttftHighAction'),
    })
  }

  // Convert upstream error ratio to percentage points and apply configured max when supplied.
  const upstreamRatePct = (overview.upstream_error_rate ?? 0) * 100
  const configuredUpstreamMax = thresholds?.upstream_error_rate_percent_max
  if (configuredUpstreamMax != null) {
    if (upstreamRatePct >= configuredUpstreamMax) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.upstreamCritical', { rate: upstreamRatePct.toFixed(2) }),
        impact: t('admin.ops.diagnosis.upstreamCriticalImpact'),
        action: t('admin.ops.diagnosis.upstreamCriticalAction'),
      })
    } else if (upstreamRatePct >= configuredUpstreamMax * 0.8) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.upstreamHigh', { rate: upstreamRatePct.toFixed(2) }),
        impact: t('admin.ops.diagnosis.upstreamHighImpact'),
        action: t('admin.ops.diagnosis.upstreamHighAction'),
      })
    }
  } else if (upstreamRatePct > 5) {
    report.push({
      type: 'critical',
      message: t('admin.ops.diagnosis.upstreamCritical', { rate: upstreamRatePct.toFixed(2) }),
      impact: t('admin.ops.diagnosis.upstreamCriticalImpact'),
      action: t('admin.ops.diagnosis.upstreamCriticalAction'),
    })
  } else if (upstreamRatePct > 2) {
    report.push({
      type: 'warning',
      message: t('admin.ops.diagnosis.upstreamHigh', { rate: upstreamRatePct.toFixed(2) }),
      impact: t('admin.ops.diagnosis.upstreamHighImpact'),
      action: t('admin.ops.diagnosis.upstreamHighAction'),
    })
  }

  // Convert request error ratio to percentage points and apply configured max when supplied.
  const errorPct = (overview.error_rate ?? 0) * 100
  const configuredErrorMax = thresholds?.request_error_rate_percent_max
  if (configuredErrorMax != null) {
    if (errorPct >= configuredErrorMax) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.errorHigh', { rate: errorPct.toFixed(2) }),
        impact: t('admin.ops.diagnosis.errorHighImpact'),
        action: t('admin.ops.diagnosis.errorHighAction'),
      })
    } else if (errorPct >= configuredErrorMax * 0.8) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.errorElevated', { rate: errorPct.toFixed(2) }),
        impact: t('admin.ops.diagnosis.errorElevatedImpact'),
        action: t('admin.ops.diagnosis.errorElevatedAction'),
      })
    }
  } else if (errorPct > 3) {
    report.push({
      type: 'critical',
      message: t('admin.ops.diagnosis.errorHigh', { rate: errorPct.toFixed(2) }),
      impact: t('admin.ops.diagnosis.errorHighImpact'),
      action: t('admin.ops.diagnosis.errorHighAction'),
    })
  } else if (errorPct > 0.5) {
    report.push({
      type: 'warning',
      message: t('admin.ops.diagnosis.errorElevated', { rate: errorPct.toFixed(2) }),
      impact: t('admin.ops.diagnosis.errorElevatedImpact'),
      action: t('admin.ops.diagnosis.errorElevatedAction'),
    })
  }

  // Convert SLA ratio to percentage points and apply configured minimum when supplied.
  const slaPct = (overview.sla ?? 0) * 100
  const configuredSlaMin = thresholds?.sla_percent_min
  if (configuredSlaMin != null) {
    if (slaPct < configuredSlaMin) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.slaCritical', { sla: slaPct.toFixed(2) }),
        impact: t('admin.ops.diagnosis.slaCriticalImpact'),
        action: t('admin.ops.diagnosis.slaCriticalAction'),
      })
    } else if (slaPct < configuredSlaMin + 0.1) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.slaLow', { sla: slaPct.toFixed(2) }),
        impact: t('admin.ops.diagnosis.slaLowImpact'),
        action: t('admin.ops.diagnosis.slaLowAction'),
      })
    }
  } else if (slaPct < 90) {
    report.push({
      type: 'critical',
      message: t('admin.ops.diagnosis.slaCritical', { sla: slaPct.toFixed(2) }),
      impact: t('admin.ops.diagnosis.slaCriticalImpact'),
      action: t('admin.ops.diagnosis.slaCriticalAction'),
    })
  } else if (slaPct < 98) {
    report.push({
      type: 'warning',
      message: t('admin.ops.diagnosis.slaLow', { sla: slaPct.toFixed(2) }),
      impact: t('admin.ops.diagnosis.slaLowImpact'),
      action: t('admin.ops.diagnosis.slaLowAction'),
    })
  }

  const healthScore =
    typeof overview.health_score === 'number' && Number.isFinite(overview.health_score)
      ? overview.health_score
      : null
  if (healthScore != null) {
    if (healthScore < 60) {
      report.push({
        type: 'critical',
        message: t('admin.ops.diagnosis.healthCritical', { score: healthScore }),
        impact: t('admin.ops.diagnosis.healthCriticalImpact'),
        action: t('admin.ops.diagnosis.healthCriticalAction'),
      })
    } else if (healthScore < 90) {
      report.push({
        type: 'warning',
        message: t('admin.ops.diagnosis.healthLow', { score: healthScore }),
        impact: t('admin.ops.diagnosis.healthLowImpact'),
        action: t('admin.ops.diagnosis.healthLowAction'),
      })
    }
  }

  if (report.length === 0) {
    report.push({
      type: 'info',
      message: t('admin.ops.diagnosis.healthy'),
      impact: t('admin.ops.diagnosis.healthyImpact'),
    })
  }

  return report
}
