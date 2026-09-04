const AUTH_TOKEN_STORAGE_KEY = "tesseract.auth.bearer-token";

export type AuthIssueKind = "credentials" | "scope";

export interface AuthIssue {
  kind: AuthIssueKind;
  message: string;
}

export interface AuthSessionState {
  hasToken: boolean;
  storage: "session" | "memory";
  issue: AuthIssue | null;
  /** Changes only when the request credential changes. */
  credentialVersion: number;
  /** Changes for every user-visible auth state update. */
  version: number;
}

interface AuthRequestCredential {
  authorization: string | null;
  credentialVersion: number;
}

type AuthSessionListener = (state: AuthSessionState) => void;

let tokenLoaded = false;
let bearerToken: string | null = null;
let storage: AuthSessionState["storage"] = "session";
let issue: AuthIssue | null = null;
let credentialVersion = 0;
let version = 0;
const listeners = new Set<AuthSessionListener>();

function loadToken(): void {
  if (tokenLoaded) return;
  tokenLoaded = true;

  if (typeof window === "undefined") {
    storage = "memory";
    return;
  }

  try {
    const storedToken = window.sessionStorage.getItem(AUTH_TOKEN_STORAGE_KEY);
    if (storedToken !== null) {
      try {
        bearerToken = normalizedToken(storedToken);
      } catch {
        window.sessionStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
      }
    }
  } catch {
    // Some privacy modes deny web storage. Retain the credential only in this
    // page's memory in that case rather than expanding the persistence scope.
    storage = "memory";
  }
}

function snapshot(): AuthSessionState {
  loadToken();
  return {
    hasToken: bearerToken !== null,
    storage,
    issue,
    credentialVersion,
    version,
  };
}

function notify(credentialChanged: boolean): AuthSessionState {
  if (credentialChanged) credentialVersion += 1;
  version += 1;
  const state = snapshot();
  for (const listener of listeners) listener(state);
  return state;
}

function normalizedToken(rawToken: string): string {
  const token = rawToken.trim();
  if (!token) throw new Error("Enter a bearer token.");
  const hasControlCharacter = [...token].some((character) => {
    const codePoint = character.codePointAt(0) ?? 0;
    return codePoint < 0x20 || codePoint === 0x7f;
  });
  if (/\s/u.test(token) || hasControlCharacter) {
    throw new Error("Bearer tokens cannot contain whitespace or control characters.");
  }
  return token;
}

export function getAuthSessionState(): AuthSessionState {
  return snapshot();
}

export function subscribeAuthSession(listener: AuthSessionListener): () => void {
  listeners.add(listener);
  // Close the render-to-effect gap for consumers that subscribe after a
  // request has already updated auth state.
  listener(snapshot());
  return () => {
    listeners.delete(listener);
  };
}

export function saveBearerToken(rawToken: string): AuthSessionState {
  const nextToken = normalizedToken(rawToken);
  loadToken();
  bearerToken = nextToken;
  issue = null;

  if (typeof window !== "undefined") {
    try {
      window.sessionStorage.setItem(AUTH_TOKEN_STORAGE_KEY, nextToken);
      storage = "session";
    } catch {
      storage = "memory";
    }
  } else {
    storage = "memory";
  }

  return notify(true);
}

export function clearBearerToken(): AuthSessionState {
  loadToken();
  bearerToken = null;
  issue = null;

  if (typeof window !== "undefined") {
    try {
      window.sessionStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
      storage = "session";
    } catch {
      storage = "memory";
    }
  }

  return notify(true);
}

/** Internal request-facing view that never exposes the raw token to UI state. */
export function getAuthRequestCredential(): AuthRequestCredential {
  loadToken();
  return {
    authorization: bearerToken ? `Bearer ${bearerToken}` : null,
    credentialVersion,
  };
}

export function reportAuthFailure(
  status: number,
  code: string | undefined,
  requestCredentialVersion: number,
): void {
  // Ignore a response sent with a credential that the operator has already
  // replaced or cleared.
  if (requestCredentialVersion !== credentialVersion) return;

  if (status === 401) {
    issue = {
      kind: "credentials",
      message: bearerToken
        ? "The server rejected this session token. Replace it or clear it and try again."
        : "This server requires a bearer token. Add one to use protected data and actions.",
    };
    notify(false);
    return;
  }

  if (status === 403 && code === "insufficient_scope") {
    issue = {
      kind: "scope",
      message:
        "This token lacks the required scope. Admin settings and configuration changes require a managed token with the admin scope.",
    };
    notify(false);
  }
}
