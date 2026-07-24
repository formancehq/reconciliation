/**
 * Canonical design-system class strings shared across the app, so the same
 * visual pattern is expressed identically everywhere (and only changes in one
 * place). Import these instead of re-typing the class strings.
 */

// ── Primary navigation tab bar ──────────────────────────────────────────────
/** Bordered row wrapping a primary TabsList (flush under the header). */
export const TABBAR_ROW = 'shrink-0 border-b px-2 py-1.5';
/** Primary TabsList. */
export const TABBAR_LIST = 'h-9 w-full touch-pan-x justify-start overflow-x-auto overscroll-x-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden';
/** Primary TabsTrigger. */
export const TABBAR_TRIGGER = 'cursor-pointer gap-1.5 text-xs';
/** Icon inside a primary TabsTrigger. */
export const TABBAR_ICON = 'h-4 w-4 shrink-0';

// ── Toolbars ────────────────────────────────────────────────────────────────
/** Filter / action toolbar card sitting above a tab's content. */
export const FILTER_TOOLBAR = 'flex flex-wrap items-center gap-3 rounded-md border p-3';

// ── Explorer tab scrolling ──────────────────────────────────────────────────
export const V3_VIEWPORT = 'h-[70vh] min-h-[480px]';
export const V3_DESKTOP_VIEWPORT = '2xl:h-[70vh] 2xl:min-h-[480px]';

// ── Semantic row highlights ─────────────────────────────────────────────────
export const REVERSAL_ROW = 'bg-yellow-background/50';
