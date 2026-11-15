// Package usage stores request analysis rows and serves statistics
// for users, keys, providers and administrator dashboards. Financial effects
// are recorded by billing; usage rows and rebuildable aggregates supply
// reports, display data and diagnostic lookup.
//
// Start with service.go for scoped queries and dashboard_service.go for
// administrator statistics and cached charts.
// dashboard_aggregation_service.go coordinates live aggregation, historical
// coverage and deletion repair, while cleanup_service.go manages persisted
// deletion tasks.
//
// log*.go, request_type.go, request_source.go and views.go define records, query results and
// loaded display relations. system_usage.go prices internal observations and
// submits immutable records to provider-only settlement.
// repository.go, query_readers.go and options.go
// define storage capabilities and construction inputs. dashboard*.go and
// query_cache.go own query caches and aggregation work.
// analytics_aggregation.go and aggregation_state.go define rollup storage and
// field-specific progress updates. cleanup*.go covers task creation,
// cancellation and execution. export*.go manages persisted CSV export tasks,
// signed download tickets and streaming encoders. provider*.go describes
// provider costs and token windows. ranking.go, runtime_settings.go,
// settings_participant.go and admin_settings_read.go control ranking visibility
// and user error views.
package usage
