import assert from "node:assert/strict";
import { after, beforeEach, test } from "node:test";

class MemorySessionStorage implements Storage {
  readonly values = new Map<string, string>();

  get length(): number {
    return this.values.size;
  }

  clear(): void {
    this.values.clear();
  }

  getItem(key: string): string | null {
    return this.values.get(key) ?? null;
  }

  key(index: number): string | null {
    return [...this.values.keys()][index] ?? null;
  }

  removeItem(key: string): void {
    this.values.delete(key);
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value);
  }
}

const sessionStorage = new MemorySessionStorage();
const location = { search: "" };
const originalWindow = globalThis.window;
const originalFetch = globalThis.fetch;

Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { location, sessionStorage },
});

const {
  clearBearerToken,
  getAuthRequestCredential,
  getAuthSessionState,
  reportAuthFailure,
  saveBearerToken,
  subscribeAuthSession,
} = await import("./api/auth.ts");
const { APIError, createAdminConfigBackup, getHealth, listNamespaces } = await import(
  "./api/client.ts"
);

beforeEach(() => {
  location.search = "";
  clearBearerToken();
});

after(() => {
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: originalWindow,
  });
  globalThis.fetch = originalFetch;
});

test("stores a validated token only in the browser session boundary", () => {
  assert.throws(() => saveBearerToken("   "), /Enter a bearer token/);
  assert.throws(() => saveBearerToken("not a token"), /cannot contain whitespace/);

  const state = saveBearerToken("  session-token  ");
  assert.equal(state.hasToken, true);
  assert.equal(state.storage, "session");
  assert.equal(sessionStorage.values.size, 1);
  assert.equal([...sessionStorage.values.values()][0], "session-token");
  assert.equal(getAuthRequestCredential().authorization, "Bearer session-token");

  const cleared = clearBearerToken();
  assert.equal(cleared.hasToken, false);
  assert.equal(sessionStorage.values.size, 0);
});

test("adds Authorization to protected API calls without putting the token in the URL", async () => {
  saveBearerToken("secret-token");
  let requestedURL = "";
  let requestedHeaders = new Headers();
  globalThis.fetch = async (input, init) => {
    requestedURL = String(input);
    requestedHeaders = new Headers(init?.headers);
    return Response.json({ items: [], count: 0 });
  };

  await listNamespaces({ limit: 1 });

  assert.equal(requestedURL, "/v1/namespaces/list?limit=1");
  assert.equal(requestedURL.includes("secret-token"), false);
  assert.equal(requestedHeaders.get("Authorization"), "Bearer secret-token");
});

test("keeps loopback no-auth requests unchanged when no token is stored", async () => {
  let requestedHeaders = new Headers();
  globalThis.fetch = async (_input, init) => {
    requestedHeaders = new Headers(init?.headers);
    return Response.json({ items: [], count: 0 });
  };

  await listNamespaces();

  assert.equal(requestedHeaders.has("Authorization"), false);
  assert.equal(getAuthSessionState().issue, null);
});

test("does not send the bearer token to public readiness calls", async () => {
  saveBearerToken("secret-token");
  let requestedHeaders = new Headers();
  globalThis.fetch = async (_input, init) => {
    requestedHeaders = new Headers(init?.headers);
    return Response.json({ status: "ready" });
  };

  await getHealth();

  assert.equal(requestedHeaders.has("Authorization"), false);
});

test("turns 401 responses into actionable credential guidance", async () => {
  let observedIssue = getAuthSessionState().issue;
  const unsubscribe = subscribeAuthSession((state) => {
    observedIssue = state.issue;
  });
  globalThis.fetch = async () =>
    Response.json(
      { code: "auth_required", message: "missing or invalid bearer token" },
      { status: 401 },
    );

  await assert.rejects(
    listNamespaces(),
    (error: unknown) =>
      error instanceof APIError &&
      error.status === 401 &&
      error.message === "Authentication required. Add a session bearer token and try again.",
  );

  assert.equal(observedIssue?.kind, "credentials");
  assert.match(observedIssue?.message ?? "", /requires a bearer token/);
  unsubscribe();
});

test("keeps transport failures distinct from authentication failures", async () => {
  globalThis.fetch = async () => {
    throw new TypeError("network unavailable");
  };

  await assert.rejects(listNamespaces(), /network unavailable/);
  assert.equal(getAuthSessionState().issue, null);
});

test("explains managed admin scope failures without discarding a usable static token", async () => {
  saveBearerToken("static-token");
  globalThis.fetch = async () =>
    Response.json(
      { code: "insufficient_scope", message: "token does not have required scope" },
      { status: 403 },
    );

  await assert.rejects(
    createAdminConfigBackup(),
    (error: unknown) =>
      error instanceof APIError &&
      error.status === 403 &&
      error.message.includes("managed token with the admin scope"),
  );

  const state = getAuthSessionState();
  assert.equal(state.hasToken, true);
  assert.equal(state.issue?.kind, "scope");
});

test("ignores an auth failure from a request sent before the token was replaced", () => {
  saveBearerToken("old-token");
  const oldVersion = getAuthRequestCredential().credentialVersion;
  saveBearerToken("new-token");

  reportAuthFailure(401, "auth_required", oldVersion);

  assert.equal(getAuthSessionState().issue, null);
  assert.equal(getAuthRequestCredential().authorization, "Bearer new-token");
});

test("leaves demo mode independent of browser credentials and transport", async () => {
  location.search = "?demo=1";
  saveBearerToken("unused-token");
  globalThis.fetch = async () => {
    throw new Error("demo mode must not call fetch");
  };

  const result = await listNamespaces({ limit: 1 });

  assert.ok(result.items.length > 0);
});
