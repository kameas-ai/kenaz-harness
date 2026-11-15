/**
 * Vue adapter for the @kameas-ai/kenaz-design-system icon registry.
 *
 * The DS ships its icons as Preact function components (`ICON_REGISTRY`
 * in `@kameas-ai/kenaz-design-system/brand`). Each one is a pure function
 * of `{ size, class }` that returns a static `<svg>` vnode tree — no hooks,
 * no state — so we call it once and translate the tree into Vue `h()`
 * calls. The glyph geometry therefore stays in the DS package and is
 * resolved at build time; none of it is copied into this repository
 * (docs/adr/0001-vendor-kenaz-tokens.md, "DS assets in an OSS repo").
 *
 * `dsIcon('x')` returns a component that is a drop-in replacement for a
 * `lucide-vue-next` icon at the call sites in this tree: it accepts
 * `size` (number | string, default 24 like Lucide) and `strokeWidth`,
 * and falls attributes such as `class` / `aria-*` through to the `<svg>`.
 */
import { defineComponent, h, type Component, type VNode } from 'vue';
import { ICON_REGISTRY, KenazMark, type IconName } from '@kameas-ai/kenaz-design-system/brand';

/** The subset of a Preact vnode the DS icons produce. */
interface PreactVNode {
  type: string | ((props: Record<string, unknown>) => unknown);
  props: Record<string, unknown> & { children?: unknown };
}

function isVNode(x: unknown): x is PreactVNode {
  return typeof x === 'object' && x !== null && 'type' in x && 'props' in x;
}

function toVue(node: unknown, rootAttrs?: Record<string, unknown>): VNode | string | null {
  if (node === null || node === undefined || node === false) return null;
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (!isVNode(node)) return null;
  if (typeof node.type === 'function') return toVue(node.type(node.props), rootAttrs);
  const { children, ...own } = node.props;
  const attrs = rootAttrs ? { ...own, ...rootAttrs } : own;
  const kids = (Array.isArray(children) ? children : [children])
    .map((c) => toVue(c))
    .filter((c): c is VNode | string => c !== null);
  return h(node.type, attrs, kids);
}

const cache = new Map<IconName, Component>();

export function dsIcon(name: IconName): Component {
  const hit = cache.get(name);
  if (hit) return hit;
  const comp = defineComponent({
    name: `DsIcon_${name}`,
    props: {
      size: { type: [Number, String], default: 24 },
      strokeWidth: { type: [Number, String], default: undefined },
    },
    setup(props) {
      return () => {
        const px = Number(props.size) || 24;
        const tree = ICON_REGISTRY[name]({ size: px }) as unknown;
        return toVue(
          tree,
          props.strokeWidth !== undefined ? { 'stroke-width': props.strokeWidth } : undefined,
        );
      };
    },
  });
  cache.set(name, comp);
  return comp;
}

/**
 * The DS `KenazMark` (5x5 lattice + rune cut-out, `inline` or `hero`
 * variant), rendered through the same vnode translation as the icons.
 */
export const DsKenazMark = defineComponent({
  name: 'DsKenazMark',
  props: {
    size: { type: Number, default: 22 },
    variant: { type: String as () => 'inline' | 'hero', default: 'inline' },
  },
  setup(props) {
    return () =>
      toVue(
        (KenazMark as unknown as (p: Record<string, unknown>) => unknown)({
          size: props.size,
          variant: props.variant,
        }),
      );
  },
});
