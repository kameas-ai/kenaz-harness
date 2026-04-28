# Research Decision Log — ACP / Agent Orchestration

## Summary

- **Feature**: `acp-orchestration-01KQ17ZK` — how kaneaz-harness should speak "ACP" to orchestrate multiple agents and workflows, with emphasis on local-first execution.
- **Date**: 2026-04-25
- **Researchers**: alecfeeman, Claude (assisting)
- **Open Questions**:
  - Should kaneaz-harness *also* adopt Anthropic's Agent Client Protocol (editor↔agent) as a secondary surface so IDEs like Zed can drive harness agents? (Separate mission if yes.)
  - Do we implement A2A v1 using the official `a2a-go` SDK or write a thin native Go implementation to avoid an external dependency in `core/`?
  - Where does A2A agent discovery fit in a local-first, zero-always-on deployment model? (See "Local-first A2A deployment" below.)

---

## Landscape snapshot (as of April 2026)

The term "ACP" in this user's brief could mean at least three different things. What we found:

| Candidate | What it is | Status | Right fit for our goal? |
|---|---|---|---|
| **A2A (Agent2Agent)** — Linux Foundation LF AI & Data | Agent↔Agent protocol. JSON-RPC 2.0 over HTTP(S); sync, SSE streaming, async push. Core concepts: Agent Cards (optionally signed), Tasks, Messages, Skills. | **v1.0 released 2026-03-12.** 150+ orgs supporting (AWS, Cisco, Google, MS, Salesforce, SAP, ServiceNow). IBM's competing "ACP" merged into A2A Aug 2025. Official SDKs: Python, Go, JS, Java, .NET. | **Yes — this is the canonical answer for "ACP for orchestrating multiple agents."** |
| **Anthropic's ACP (Agent Client Protocol)** — agentclientprotocol.com | Editor↔Agent protocol (client-to-agent only — "a protocol for connecting any editor to any agent"). JSON-RPC 2.0. | v0.12.2 (2026-04-23), pre-1.0. SDKs: Kotlin, Java, Python, Rust, TypeScript. **No Go SDK.** Adopters: Claude Code, Gemini CLI, Codex CLI, Zed. | No for agent orchestration — scope is IDE integration. *Could* be a separate, optional surface later. |
| **IBM BeeAI ACP (Agent Communication Protocol)** — acp.dev | Originally agent↔agent; donated to LF in March 2025. | **Dead as a separate protocol.** Merged into A2A in August 2025 under LF AI & Data. | No — it is now A2A. |
| **Google A2A (pre-LF)** | Same as A2A above, pre-donation form. | Superseded by LF-governed A2A v1.0. | See A2A row. |

This resolves the user's framing question ("determine how the industry is starting to support acp and exactly what it means"): **the industry has consolidated on A2A v1.0 under the Linux Foundation for agent-to-agent orchestration**. The IBM ACP brand name lost; "ACP" now most often refers to Anthropic's editor-integration protocol, which is a separate concern.

---

## Decisions & Rationale

| Decision | Rationale | Evidence | Status |
|----------|-----------|----------|--------|
| **D1**: Adopt **A2A v1.0** as the protocol for orchestrating multiple agents and flows. | A2A is the LF-governed, v1.0-released (2026-03-12), multi-vendor standard that explicitly targets agent↔agent orchestration. IBM's competing ACP already merged into it; the market has consolidated. Enterprise-ready is an explicit A2A design goal, aligning with our SOC 2-ready charter. | evidence-log rows E2, E3, E5, E7; source-register `a2a-v1-home`, `a2a-v1-release`, `lf-a2a-press`, `ibm-acp-merged` | final |
| **D2**: Defer Anthropic's ACP (editor↔agent) to a separate, later mission. | Different scope (IDE integration, not orchestration). Pre-1.0. No Go SDK. Our immediate need is multi-agent orchestration, not driving Claude Code / Zed from outside. | evidence-log rows E1, E6; source-register `anthropic-acp-overview`, `anthropic-acp-github` | final |
| **D3**: Use the official **`github.com/a2aproject/a2a-go`** SDK as the A2A implementation, wrapped behind our own adapter in `core/acp/` so upgrades and swaps stay local per DIRECTIVE_001. | A first-party Go SDK exists — writing our own would duplicate work and lag behind spec changes. Wrapping it preserves architectural integrity (the rest of `core/` never imports `a2a-go` directly). | source-register `a2a-go-sdk` | final |
| **D4**: Treat A2A as the **external** orchestration surface. The **internal** orchestration layer (workflow engine executing DAG-of-steps from bundles) is a separate mission and is not defined by A2A. A2A is how we *talk to* other agents and how other agents *call* ours. | Configuration-first means bundles declare workflows in our own DSL; A2A is a wire protocol, not a workflow engine. Keeping the two concerns separate matches C4-incremental-detail-modeling and DDD boundaries. | This document, charter selected paradigms | final |
| **D5**: Support A2A in **both client and server modes** from v1: harness calls out to external A2A agents, and harness exposes its own agents/workflows as an A2A endpoint addressable by other clients (including another harness instance). | Symmetric support matches the "model-agnostic, configuration-first" charter goal. Client-only would make us a consumer of the ecosystem; server-only would be useless without peers. Both is the table-stakes posture for a harness that wants to be relevant in a 150+-org ecosystem. | evidence-log row E2 (A2A bi-directional), MCP spec already chose the same symmetric posture | final |
| **D6**: **Local-first A2A deployment**: default transport is **loopback HTTP over a Unix domain socket or localhost port**, not a public WAN endpoint. Remote/WAN exposure is opt-in, gated behind explicit configuration and credential setup. | Charter invariant: local-first, security-first, no surprise network egress (NFR-009 of llm-connector spec). A2A is HTTP-based which makes loopback trivial. Public exposure requires auth design that is beyond this mission's v1 scope. | evidence-log row E2 (JSON-RPC over HTTP), charter NFR-009 | final |
| **D7**: Defer **Signed Agent Cards** and cross-organization trust to a follow-up mission. Ship v1 with unsigned Agent Cards over loopback/LAN only. | Signed Agent Cards are an A2A v1 feature, but the broader trust/PKI story requires infrastructure we do not yet have. v1 value comes from orchestrating agents under one operator's control. A2A's own roadmap acknowledges that authorization schemes embedded in AgentCard are still being formalized. | evidence-log row E3 (Signed Agent Cards v1 feature; authorization still being formalized) | final |
| **D8**: The Python LangChain daemon (separate mission) becomes an A2A-speaking agent from the harness's point of view. The Go harness does not import LangChain; the daemon exposes A2A endpoints for each workflow it hosts. | Keeps Go/Python boundary clean. A2A is the contract; the daemon is free to use LangChain/LangGraph/anything internally. Upgrading LangChain does not touch `core/`. Matches DIRECTIVE_001. | This document, charter DIRECTIVE_001 | follow-up (depends on python-daemon mission) |

---

## Evidence Highlights

- **Key insight 1 — "ACP" is an ambiguous term; A2A is the actual standard.** IBM's ACP merged into A2A in Aug 2025; "ACP" now most commonly refers to Anthropic's unrelated editor-integration protocol. (evidence rows E1, E5, E8)
- **Key insight 2 — A2A v1.0 is production-ready and has a first-party Go SDK.** Released 2026-03-12; JSON-RPC 2.0 over HTTP(S) with sync, SSE streaming, async push; Python, Go, JS, Java, .NET SDKs; 150+ org ecosystem. (evidence rows E2, E3, E7)
- **Key insight 3 — A2A and MCP are complementary, not competing.** Official A2A docs state: "A2A acts as the public internet that allows AI agents — including those using MCP — to interoperate." MCP = agent↔tool. A2A = agent↔agent. Our harness needs both. (evidence rows E2, E4)
- **Key insight 4 — LangGraph already integrates with MCP and uses Command-based handoffs internally.** Native inter-agent messaging in LangGraph is in-process. A2A is the natural bridge when inter-process or inter-host orchestration is required — which is exactly the Go↔Python daemon situation. (evidence row E9)
- **Risks / Concerns**:
  - A2A authentication model is still being formalized (see roadmap note about "inclusion of authorization schemes and optional credentials directly within the AgentCard"). Deferring cross-org trust (D7) sidesteps this for v1 but it *will* land as a follow-up mission.
  - No A2A SDK availability visible for Rust; irrelevant today but worth noting if we ever add a Rust component.
  - "A2A over HTTP(S)" with local deployment needs a decision on UDS vs localhost TCP. UDS is cleaner on unix, worse on Windows — matters for our cross-platform charter. Planning-phase decision.

---

## Next Actions

1. Merge D1–D8 into the mission spec (spec.md) — write user stories and requirements specifically for the A2A integration as scoped here.
2. Coordinate sequencing with the `mcp-client` / `mcp-server` missions and the `workflow-engine` / `python-daemon` / `langchain-workflows` missions — A2A is the external protocol that stitches them together.
3. Decision for planning phase: UDS vs localhost TCP for default local transport (affects Windows behavior).
4. Decision for planning phase: whether `a2a-go` becomes a direct `core/acp/` dependency or we keep it behind a build tag so an enterprise variant can swap it.
5. Schedule a charter amendment (or companion ADR) once this mission is accepted, recording: (a) that A2A v1 is an endorsed protocol, (b) that Anthropic's ACP is explicitly out of scope for this mission.

> Keep this document living. Update D3, D6, D7 if A2A publishes a formal auth scheme before we plan this mission, or if the Python daemon mission lands before this one and changes the transport baseline.
