import assert from "node:assert/strict";
import test from "node:test";
import { buildRouteUrl } from "./routing.ts";

test("preserves demo mode when entering the admin route", () => {
  assert.equal(buildRouteUrl("admin", {}, "?demo=1"), "/admin?demo=1");
});

test("preserves top-level query flags when leaving admin for a hash route", () => {
  assert.equal(buildRouteUrl("dashboard", {}, "?demo=1"), "/?demo=1#dashboard");
});

test("keeps route context separate from top-level query flags", () => {
  assert.equal(
    buildRouteUrl(
      "memoryDetail",
      { domain: "memory", namespace: "user/demo", key: "favorite color" },
      "?demo=1",
    ),
    "/?demo=1#memoryDetail?namespace=user%2Fdemo&key=favorite+color&domain=memory",
  );
});
