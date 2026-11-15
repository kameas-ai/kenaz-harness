// Icon registry for the harness UI.
//
// Glyphs the Kenaz design system (@kameas-ai/kenaz-design-system v0.13.0,
// `ICON_REGISTRY`) provides are rendered from the DS through `dsIcon()`
// (see ./dsIcon.ts). The export names are kept so call sites read the
// same; the DS glyph that backs each one is noted alongside.
//
// Glyphs the DS does not have yet stay on lucide-vue-next (tree-shaken, so
// only the names below reach the bundle — NFR-006). Each is a design-system
// gap recorded in the fleet design-gaps report (harness surface); move a
// name into the DS block once the DS ships the glyph.
import type { Component } from 'vue';
import {
  AlertTriangle,
  Archive,
  CalendarClock,
  CheckSquare,
  Circle,
  CircleUser,
  Code,
  Command,
  Download,
  Flag,
  GitBranch,
  Image,
  Layers,
  Package,
  Pin,
  Route,
  Server,
  SquareTerminal,
  Sun,
  Moon,
  Pencil,
  Trash2,
  Webhook,
  Zap,
} from 'lucide-vue-next';
import { dsIcon } from './dsIcon';

// ── DS-backed (generic glyphs) ────────────────────────────────────────
export const X = dsIcon('x');
export const Check = dsIcon('check');
export const ChevronDown = dsIcon('chevronDown');
export const ChevronRight = dsIcon('chevronRight');
export const Plus = dsIcon('plus');
export const RefreshCw = dsIcon('refresh');
export const Search = dsIcon('search');
export const Lock = dsIcon('lock');
export const Shield = dsIcon('shield');
export const Folder = dsIcon('folder');
export const FolderOpen = dsIcon('folder');
export const Globe = dsIcon('globe');
export const KeyRound = dsIcon('key');
export const SlidersHorizontal = dsIcon('sliders');
export const FileText = dsIcon('document');
export const Wrench = dsIcon('tools');
export const Clipboard = dsIcon('clipboard');
export const Hash = dsIcon('hash');
export const Cpu = dsIcon('cpu');
export const Play = dsIcon('play');
export const Stop = dsIcon('stop');

// ── DS-backed (semantic harness / Kenaz glyphs) ───────────────────────
// Former Lucide stand-ins, mapped to the DS glyph for the same surface.
export const Settings = dsIcon('sliders'); //        was lucide Settings (gear)
export const MessageSquare = dsIcon('sessions'); //  was lucide MessageSquare
export const Blocks = dsIcon('tools'); //            Capabilities (= Tools view)
export const BookOpen = dsIcon('memory'); //         Knowledge
export const Brain = dsIcon('memory'); //            project memory
export const Plug = dsIcon('provider'); //           Providers
export const Scale = dsIcon('policies'); //          Policy
export const Activity = dsIcon('audit'); //          Audit log
export const Workflow = dsIcon('workflow');
export const Agent = dsIcon('agent');
export const Observer = dsIcon('observer');
export const Suggestions = dsIcon('suggestions');

// ── Lucide (DS has no glyph yet — design gaps) ────────────────────────
/**
 * Permission-family glyphs (permission prompts, the grants list). Was an
 * emoji per family; DS glyphs where the DS has one. `bash` has no DS glyph
 * (Lucide SquareTerminal, design gap).
 */
export const PERMISSION_FAMILY_ICONS: Record<string, Component> = {
  bash: SquareTerminal,
  fs: Folder,
  credential: KeyRound,
  cred: KeyRound,
  tool: Wrench,
  network: Globe,
  lock: Lock,
};

export {
  AlertTriangle,
  SquareTerminal,
  Archive,
  CalendarClock,
  CheckSquare,
  Circle,
  CircleUser,
  Code,
  Command,
  Download,
  Flag,
  GitBranch,
  Image,
  Layers,
  Package,
  Pin,
  Route,
  Server,
  Sun,
  Moon,
  Pencil,
  Trash2,
  Webhook,
  Zap,
};
