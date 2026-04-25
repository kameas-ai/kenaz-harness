// Package kind owns the open kind registry. Built-in kinds (event-log
// self-events) are pre-registered; consumers register their own under
// their namespace prefix. Unknown kinds are accepted as opaque so
// older readers stay forward-compatible (NFR-006).
package kind
