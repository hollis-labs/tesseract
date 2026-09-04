import type { NavPage } from "./components/layout/nav";

/** Navigation state encoded in a hash route. */
export interface NavContext {
  namespace?: string;
  key?: string;
  revisionA?: number;
  revisionB?: number;
  domain?: "memory" | "knowledge";
  reviewPreset?: "lowConfidence" | "reviewed" | "pendingReview";
}

/**
 * Build the browser URL for a route while retaining top-level query flags,
 * including `?demo=1`, across the special `/admin` path boundary.
 */
export function buildRouteUrl(page: NavPage, ctx: NavContext, search: string): string {
  if (page === "admin" && Object.keys(ctx).length === 0) {
    return `/admin${search}`;
  }

  const params = new URLSearchParams();
  if (ctx.namespace) params.set("namespace", ctx.namespace);
  if (ctx.key) params.set("key", ctx.key);
  if (ctx.domain) params.set("domain", ctx.domain);
  if (ctx.revisionA != null) params.set("revisionA", String(ctx.revisionA));
  if (ctx.revisionB != null) params.set("revisionB", String(ctx.revisionB));
  if (ctx.reviewPreset) params.set("reviewPreset", ctx.reviewPreset);
  const routeQuery = params.toString();
  const hash = routeQuery ? `#${page}?${routeQuery}` : `#${page}`;
  return `/${search}${hash}`;
}
