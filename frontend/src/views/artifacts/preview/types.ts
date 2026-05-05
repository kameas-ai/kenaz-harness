/**
 * types.ts — shared type definitions for the artifact binary-preview
 * renderer registry (artifact-preview-binary-rendering-01KQ8TD5).
 */

import type { Component } from 'vue';
import type { ArtifactWithBytes } from '@/lib/types';

/** Discriminant used by the parent to log / test which path was taken. */
export type RendererKind =
  | 'image'
  | 'pdf'
  | 'audio'
  | 'video'
  | 'html'
  | 'markdown-inline'
  | 'markdown-iframed'
  | 'text'
  | 'unknown-binary';

/** What the registry returns for a given artifact. */
export interface RendererSpec {
  kind: RendererKind;
  component: Component;
  /** When true the parent should enforce the byte-size / time cap. */
  enforceCap: boolean;
}

/**
 * Props every renderer component must accept.
 * The parent (ArtifactPreview.vue) owns abort wiring; renderers are thin.
 */
export interface RendererProps {
  artifact: ArtifactWithBytes['artifact'];
  /** Raw base-64 bytes as returned by Artifacts_Get. */
  bytesB64: string;
  /** Object-URL or data-URL the renderer may bind to <img src>, <audio src>, etc. */
  sourceUrl: string;
  /** AbortSignal fired when the byte-cap or time-cap trips. */
  abortSignal: AbortSignal;
  /** Called by the renderer when it detects a load error (e.g. img onerror). */
  onSizeExceeded: () => void;
  /** Called by the renderer when a timeout fires internally. */
  onTimeout: () => void;
}
