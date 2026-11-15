// Package postgres persists analytical usage records and reads retained reports.
// Acting users, billing users, diagnostic request IDs, and financial deduplication
// keys keep separate meanings across queries; billing transactions establish the
// actual funding outcome recorded for analysis.
//
// Reading entry points:
//   - usage_log_repo.go constructs the store, coordinates batcher shutdown, and
//     defines common compatibility filters and public ranking queries.
//   - usage_log_repo_insert.go implements queued and direct writes, result
//     acknowledgement, duplicate identity recovery, and batch failure handling.
//   - usage_log_repo_query.go implements scope selection, pagination, row scanning,
//     and current display-association hydration.
//
// File groups:
//   - usage_log_repo*.go covers writes, listings, statistics, trends, dashboards,
//     and multidimensional analytics queries with raw source recovery.
//   - dashboard_aggregation_repo.go and usage_analytics_aggregation_repo.go
//     aggregate, backfill, and clean reconstructible report data.
//   - provider_report.go and team_reports.go read provider and team reports.
//   - api_key_dashboard_analytics.go and key_totals.go read key dashboard metrics
//     and retained usage totals.
//   - usage_cleanup_repo.go persists cleanup tasks and deletes analytical rows.
//   - usage_export_repo.go persists export tasks, worker claims, and expiry state.
//   - support.go delegates SQL helpers and owning-module display conversions.
package postgres
