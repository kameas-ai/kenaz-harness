/**
 * Shared recipe-category taxonomy (FR-005).
 *
 * Single source of truth for the 16 canonical MCP-connector categories:
 * their display names and their icons. Both the Registry catalog
 * (`RegistryTab.vue`) and the built-in Tools panel (`KenazToolsPanel.vue`)
 * resolve icons + labels through here instead of maintaining parallel
 * `switch` statements.
 *
 * The taxonomy is intentionally tolerant of unknown / legacy category
 * strings: anything outside the canonical set renders with a title-cased
 * label and the generic `Wrench` icon rather than being dropped or
 * collapsed. This keeps the catalog forward-compatible with registry
 * data that adds categories ahead of the frontend.
 */
import {
  Activity,
  CheckSquare,
  CircleUser,
  Code,
  Flag,
  Folder,
  Globe,
  Image,
  Layers,
  MessageSquare,
  Package,
  Route,
  Scale,
  Server,
  Shield,
  Wrench,
  Zap,
} from '@/shell/icons';
import type { CanonicalRecipeCategory, RecipeCategory } from '@/lib/types';

/** All icon components share lucide's component signature. */
type IconComponent = typeof Wrench;

/** The 16 canonical categories the registry normalizes to. */
export const CANONICAL_RECIPE_CATEGORIES: readonly CanonicalRecipeCategory[] = [
  'automation',
  'communication',
  'crm',
  'data',
  'design',
  'developer',
  'ecommerce',
  'files',
  'finance',
  'hr_people',
  'marketing',
  'observability',
  'productivity',
  'security',
  'support',
  'web',
];

interface CategoryDef {
  /** Section header / label shown in the UI. */
  label: string;
  /** Lucide icon component. */
  icon: IconComponent;
}

/**
 * Canonical value → { display name, icon }. Display names match the
 * taxonomy agreed with the connector-catalog data normalization.
 */
export const RECIPE_CATEGORY_META: Readonly<
  Record<CanonicalRecipeCategory, CategoryDef>
> = {
  automation: { label: 'Automation & iPaaS', icon: Zap },
  communication: { label: 'Communication', icon: MessageSquare },
  crm: { label: 'CRM & Sales', icon: Route },
  data: { label: 'Data & BI', icon: Layers },
  design: { label: 'Design', icon: Image },
  developer: { label: 'Developer', icon: Code },
  ecommerce: { label: 'E-commerce & Payments', icon: Package },
  files: { label: 'Files & Storage', icon: Folder },
  finance: { label: 'Finance & Accounting', icon: Scale },
  hr_people: { label: 'HR & People', icon: CircleUser },
  marketing: { label: 'Marketing & Analytics', icon: Flag },
  observability: { label: 'Observability & CI', icon: Activity },
  productivity: { label: 'Productivity & Projects', icon: CheckSquare },
  security: { label: 'Security', icon: Shield },
  support: { label: 'Support & ITSM', icon: Server },
  web: { label: 'Web & Search', icon: Globe },
};

/** True when `category` is one of the 16 canonical values. */
export function isCanonicalCategory(
  category: RecipeCategory,
): category is CanonicalRecipeCategory {
  return Object.prototype.hasOwnProperty.call(RECIPE_CATEGORY_META, category);
}

/** Title-case a raw category slug: `hr_people` → `Hr People`. */
function titleCase(raw: string): string {
  return raw
    .split(/[_\-\s]+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

/**
 * Display label for any category. Canonical values use their curated
 * display name; empty/undefined categories fall back to "Other"; unknown
 * slugs are title-cased.
 */
export function categoryLabel(category: RecipeCategory): string {
  if (isCanonicalCategory(category)) return RECIPE_CATEGORY_META[category].label;
  const raw = String(category ?? '').trim();
  return raw ? titleCase(raw) : 'Other';
}

/**
 * Icon component for any category. Canonical values use their curated
 * icon; anything else falls back to the generic `Wrench`.
 */
export function categoryIconFor(category: RecipeCategory): IconComponent {
  return isCanonicalCategory(category)
    ? RECIPE_CATEGORY_META[category].icon
    : Wrench;
}
