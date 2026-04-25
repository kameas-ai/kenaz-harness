/**
 * Hand-curated TS mirror of the Go-side payloads exposed via Wails. The
 * canonical shapes live in core/rpc/{api.go, views/*}; these mirrors keep
 * the frontend strict-typed without leaking wailsjs/go/models into
 * components (FR-019, NFR-007).
 *
 * NO `any` here — privacy CI invariant + ESLint enforce.
 */

export interface Session {
  id: string;
  name: string;
  createdAt: string;
  updatedAt: string;
}

export interface Provider {
  id: string;
  name: string;
  tier: string;
  model: string;
}

export interface MCPServer {
  id: string;
  name: string;
  state: string;
  version: string;
  transport?: string;
  capabilities?: string[];
}

export interface A2ACard {
  id: string;
  issuer: string;
  subject: string;
  issuedAt: string;
}

export interface Job {
  id: string;
  name: string;
  state: string;
  startedAt?: string;
}

/**
 * Reference-only secret metadata. Compile-time guard: NO `value` /
 * `secret` / `password` / `apiKey` / `token` field — the WP14 lint rule
 * flags any addition. FR-020 / C-004 / privacy CI invariant.
 */
export interface SecretReference {
  id: string;
  label: string;
  source: string;
  createdAt: string;
}

export interface ContextEntry {
  id: string;
  kind: string;
  label: string;
}

export interface Bundle {
  id: string;
  name: string;
  version: string;
  tier: string;
  source?: string;
  signature?: string;
  artifactCount: number;
  artifacts?: BundleArtifact[];
}

export interface BundleArtifact {
  name: string;
  kind: string;
  contentHash: string;
}

export interface Denial {
  policyId: string;
  clauseId: string;
  violatingInput: string;
  remediation: string;
}

export interface AuditEntry {
  id: string;
  timestamp: string;
  category: string;
  subject: string;
  trailing?: string;
}

export interface AuditFilter {
  categories?: string[];
  since?: string;
  until?: string;
  limit?: number;
}

export interface ShellStatus {
  activeProvider: string;
  trustTier: string;
  harnessBuild: string;
  connection: ConnectionState;
  eventRate: number;
  policyApplied: boolean;
  redactionOn: boolean;
  localFirstOn: boolean;
}

export interface AppInfo {
  build: string;
  commit: string;
  buildTime: string;
  goVersion: string;
  platform: string;
  windowSize: WindowSize;
}

export interface WindowSize {
  width: number;
  height: number;
}

export interface Settings {
  schemaVersion: number;
  lastRoute: string;
  theme: Theme;
  accent: string;
  windowSize: WindowSize;
}

export type Theme = 'light' | 'dark' | 'system';

export type ConnectionState = 'connecting' | 'ready' | 'degraded' | 'lost';

/**
 * EventStreamEntry — the typed shape rendered by EventStreamRow. The
 * `category` is constrained to the registry in lib/categories.ts.
 */
export interface EventStreamEntry {
  id: string;
  timestamp: string;
  category: string;
  subject: string;
  trailing?: string;
  size?: number;
}
