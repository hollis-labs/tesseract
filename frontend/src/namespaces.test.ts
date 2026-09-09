import assert from "node:assert/strict";
import { after, test } from "node:test";

// Demo mode is selected off window.location.search, so the window has to exist
// before the modules under test are imported.
const location = { search: "?demo=1" };
const originalWindow = globalThis.window;

Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { location, sessionStorage: undefined },
});

const { demo } = await import("./demo/data.ts");
const { listAllNamespaces } = await import("./api/client.ts");

after(() => {
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: originalWindow,
  });
});

// Demo mode exists so UI work can be reviewed without a daemon. An argument it
// accepts that the server rejects lets a bug through review and fails in
// production; the widened-filter case is the worst, because the page still
// renders, just over the wrong set.
test("demo mode refuses the arguments the server refuses", () => {
  assert.throws(
    () => demo.listNamespaces({ prefix: "user/", match: "jane" }),
    /two spellings of the same filter/,
  );
  assert.throws(
    // @ts-expect-error deliberately outside the union, which is what a stale
    // caller or a hand-built query string would send.
    () => demo.listNamespaces({ match: "user/", match_mode: "regex" }),
    /unknown match mode "regex".*prefix\|contains\|glob/,
  );
  assert.throws(
    // @ts-expect-error deliberately outside the union.
    () => demo.listNamespaces({ sort: "owner_id" }),
    /unknown sort "owner_id".*namespace\|owner\|updated_at/,
  );
  assert.throws(
    // @ts-expect-error deliberately outside the union.
    () => demo.listNamespaces({ dir: "sideways" }),
    /dir must be asc or desc/,
  );
  assert.throws(
    () => demo.listNamespaces({ cursor: "not-a-cursor" }),
    /not a valid pagination token/,
  );
});

// The conflict message names a mode the caller can actually re-issue with, so
// the vocabulary check has to come first.
test("demo mode rejects an unknown mode before suggesting it back", () => {
  assert.throws(
    // @ts-expect-error deliberately outside the union.
    () => demo.listNamespaces({ prefix: "user/", match_mode: "regex" }),
    (err: Error) => /unknown match mode/.test(err.message) && !/instead/.test(err.message),
  );
  assert.throws(
    () => demo.listNamespaces({ prefix: "user/", match_mode: "glob" }),
    /pass match="user\/" with match_mode=glob instead/,
  );
});

test("demo mode still answers the supported arguments", () => {
  const all = demo.listNamespaces();
  assert.ok(all.count > 0);
  assert.equal(all.items.length, all.count);
  assert.equal(all.truncated, false);
  assert.equal(all.next_cursor, undefined);

  const prefixed = demo.listNamespaces({ prefix: "user/jane/" });
  assert.ok(prefixed.count > 0);
  assert.ok(prefixed.items.every((n) => n.namespace.startsWith("user/jane/")));

  const globbed = demo.listNamespaces({ match: "app/*/session*", match_mode: "glob" });
  assert.ok(globbed.items.every((n) => n.namespace.startsWith("app/")));

  const desc = demo.listNamespaces({ dir: "desc" });
  assert.deepEqual(
    desc.items.map((n) => n.namespace),
    [...all.items.map((n) => n.namespace)].reverse(),
  );
});

// The bug the whole change exists to remove: a caller that takes one capped
// response for the whole set. The helper must page until the server stops
// issuing cursors, and report the complete set.
test("listAllNamespaces pages a capped listing to completion", async () => {
  const total = demo.listNamespaces().count;
  assert.ok(total > 2, "demo fixture needs enough namespaces to page");

  // One page at a time is the strongest version of the same walk the real
  // helper does at limit=1000.
  let cursor: string | undefined;
  const walked: string[] = [];
  for (let page = 0; page < total + 1; page++) {
    const response = demo.listNamespaces(
      cursor === undefined ? { limit: 1 } : { limit: 1, cursor },
    );
    walked.push(...response.items.map((n) => n.namespace));
    assert.equal(response.count, total, "count is the whole match, not the page");
    assert.equal(response.truncated, response.next_cursor !== undefined);
    if (!response.next_cursor) break;
    cursor = response.next_cursor;
  }
  assert.equal(walked.length, total);
  assert.equal(new Set(walked).size, total, "a namespace was returned on more than one page");

  const complete = await listAllNamespaces();
  assert.equal(complete.complete, true);
  assert.equal(complete.count, total);
  assert.equal(complete.items.length, total);
});
