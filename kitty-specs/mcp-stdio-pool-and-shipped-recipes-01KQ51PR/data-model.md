# Data Model — MCP stdio pool + shipped recipes

## Recipe (catalog entry — `core/mcp/recipes/recipes.go`)

```
Recipe
├── ID              string         // "brave-search" — primary key in catalog
├── DisplayName     string         // "Brave Search"
├── Description     string         // user-visible copy
├── Category        string         // "search" | "filesystem" | "memory" | "fetch" | …
├── Command         []string       // ["npx","-y","@modelcontextprotocol/server-brave-search"]
├── EnvKeys         []EnvKey       // ordered, render order in modal = slice order
├── Capabilities    Capabilities   // declared (cross-checked against initialize response)
├── DocsURL         string
├── InitTimeoutMs   int            // 0 → default 5000 (response deadline post-spawn)
├── PingPeriodMs    int            // 0 → default 30000
└── SamplingPolicy  SamplingPolicy // {Allowed bool, Default bool}  — default {false, false}
```

Relationships: 1 `Recipe` → 0..n `EnvKey`s (composition). `Capabilities` embedded.

Validation: `ID` matches `^[a-z][a-z0-9-]{0,63}$` (becomes `mcp.Tool.Server` and a keychain locator prefix). `Command[0]` must be non-empty.

## EnvKey

```
EnvKey
├── Name      string  // "BRAVE_API_KEY" — exact env var the server reads
├── Display   string  // "Brave Search API Key" — modal label
├── DocsURL   string  // "https://api.search.brave.com/app/keys"
└── Required  bool
```

## Capabilities

```
Capabilities
├── Tools     bool   // declared client expectation; harness sends tools/list anyway
├── Resources bool
├── Prompts   bool
└── Sampling  bool   // does the server *want* to use sampling? Recipe-author hint
```

Recipe-author declaration; the **negotiated** set lives on `ServerInstance.Negotiated` after `initialize` returns.

## RecipeStatus (live snapshot — returned by `Tools_RecipeStatus`)

```
RecipeStatus
├── ID                string
├── Enabled           bool          // present in recipes.enabled.json
├── State             string        // "stopped" | "starting" | "running" | "restarting" | "failed"
├── LastError         string        // surfaced from stderr ring buffer or initialize error
├── RestartAttempts   int           // attempts inside the current 5-min window
├── LastRestartAt     time.Time     // zero if never restarted
├── KeysPresent       bool          // every required EnvKey resolves through secrets.Backend
├── PID               int           // 0 when not running
├── ProtocolVersion   string        // server-reported, post-initialize
├── ServerName        string        // server-reported, post-initialize
├── ServerVersion     string        // server-reported, post-initialize
├── ToolCount         int
├── ResourceCount     int
├── PromptCount       int
├── StderrTail        string        // last 4 KiB of the ring buffer
└── UpdatedAt         time.Time
```

## EnabledRecipes (persisted — `<DataDir>/mcp/recipes.enabled.json`)

```
EnabledRecipes
└── Entries []EnabledRecipe
    ├── ID               string    // FK → Recipe.ID
    ├── EnabledAt        time.Time // RFC3339
    ├── SamplingEnabled  bool      // per-server toggle from FR-015
    └── EnvAuditHash     string    // sha256 of all env-key locators (NOT values) — change-detect
```

Persistence rules: atomic write via tmpfile + `os.Rename`. fsync the parent directory after rename. On corruption (parse error), log + start with empty list per spec.md §9 edge case 7.

## ServerInstance (in-memory — `core/mcp/stdio/server.go`)

```
ServerInstance
├── recipe         *Recipe
├── cmd            *exec.Cmd
├── stdin          io.WriteCloser
├── stdout         io.ReadCloser
├── stderr         *RingBuffer       // 64 KiB
├── framer         *Framer           // owns the bufio.Scanner + json.Encoder
├── router         *ResponseRouter   // map[id]chan rawMessage
├── nextID         atomic.Int64
├── notifTopic     chan Notification // fan-out to log/progress/etc handlers
├── samplingMu     sync.RWMutex
├── samplingOn     bool
├── samplingProxy  SamplingHandler   // adapter onto core/llm
├── rootsProxy     RootsHandler
├── lifecycleMu    sync.Mutex
├── state          State
├── restartHistory []time.Time       // append on restart, prune entries > 5min old
├── initOnce       sync.Once
├── closeOnce      sync.Once
├── doneCh         chan struct{}     // closed when reader+writer goroutines exit
└── negotiated     Capabilities      // result of initialize handshake
```

State machine: `stopped → starting → running → restarting → running` or `→ failed`. `failed` only resets on user toggle.

## RequestEnvelope (wire shape — internal)

```
RequestEnvelope
├── JSONRPC string  // "2.0"
├── ID      *int64  // nil for notifications
├── Method  string
├── Params  json.RawMessage
└── Error   *RPCError  // for response envelopes
```

## ProtocolVersion

```
const SupportedProtocolVersion = "2024-11-05"   // pinned; bumped per WP when MCP spec advances
```

## RingBuffer

```
RingBuffer  (core/mcp/stdio/internal/ringbuf or inline)
├── buf  []byte (length 64 KiB)
├── pos  int
├── full bool
└── mu   sync.Mutex

methods:
  Write(p []byte) (n int, err error)  // never returns err
  Snapshot(maxBytes int) string
```

## Relationships to existing types

- **`core/mcp.Tool`** — `ServerInstance` produces these by translating the server's `tools/list` response. `Tool.Server` is set to `Recipe.ID` (so `kaneaz.brave_web_search` becomes `Server="brave-search", Name="brave_web_search"` — collision-free namespacing per spec.md §9 edge case 6).
- **`core/mcp.ServerSpec`** — `recipes.go` exposes `(*Recipe).ToServerSpec(env map[string]string) mcp.ServerSpec` so the existing pool surface continues to take `[]ServerSpec` in `Pool.Open`.
- **`core/toolloop.MCPPool` adapter** (`core/rpc/api.go::mcpPoolAdapter`) — unchanged; it already speaks the `Tools()/Call()` subset.
- **`core/secrets.Backend`** — keychain locator scheme: `mcp/<recipe-id>/<env-name>` (e.g., `mcp/brave-search/BRAVE_API_KEY`). The locator is the input to `EnabledRecipes.EnvAuditHash`'s sha256 — never the value.
