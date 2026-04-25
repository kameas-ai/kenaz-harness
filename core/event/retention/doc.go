// Package retention covers the keep_all default plus the v1.x archive
// and truncate operations. Retention operations are explicit and
// themselves recorded in the chain (event-log.retention.started /
// event-log.retention.completed); the active log never silently loses
// data.
package retention
