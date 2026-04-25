// Package storage is the harness's persistent substrate.
//
// All storage code lives under core/storage/... and is the only place
// the harness imports SQLite, libSQL, sqlite-vec, LanceDB, or any other
// vector backend SDKs (DIRECTIVE_001). Other core/ packages consume the
// public Go interfaces declared in this package and never touch a driver
// directly.
//
// Package layout (mirrors plan §2):
//
//	core/storage/
//	├── storage.go                 // public types, errors, Open entry point
//	├── eventkinds.go              // canonical storage-event kind constants
//	├── db/                        // libSQL connection management
//	├── migrations/                // versioned migration framework + ledger
//	├── vector/                    // vector-store abstraction + backends
//	│   ├── sqlitevec/             // default backend
//	│   ├── lancedb/               // opt-in (build tag: harness_lancedb)
//	│   └── chromem/               // opt-in pure-Go (build tag: harness_chromem)
//	├── backup/                    // online backup + restore
//	├── diagnostics/               // integrity check + status surface
//	└── internal/
//	    ├── lockfile/              // PID lock for single-writer enforcement
//	    └── events/                // buffered EventSink implementation
//
// SQLite floor: 3.51.0 (per research D5 — Windows NTFS WAL close() lock
// bug below this version). libSQL releases tracked accordingly.
package storage
