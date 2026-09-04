import { Button, Callout, FormDialog, Input, Label, Pill } from "@hollis-labs/sysop-ui";
import { KeyRound, ShieldAlert, ShieldCheck, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import {
  type AuthSessionState,
  clearBearerToken,
  getAuthSessionState,
  saveBearerToken,
  subscribeAuthSession,
} from "../../api/auth";

interface Props {
  onCredentialChange: (credentialVersion: number) => void;
}

export function AuthSessionControl({ onCredentialChange }: Props) {
  const [auth, setAuth] = useState<AuthSessionState>(getAuthSessionState);
  const [open, setOpen] = useState(false);
  const [token, setToken] = useState("");
  const [validationError, setValidationError] = useState<string | null>(null);

  useEffect(
    () =>
      subscribeAuthSession((next) => {
        setAuth(next);
        onCredentialChange(next.credentialVersion);
      }),
    [onCredentialChange],
  );

  useEffect(() => {
    if (auth.issue) setOpen(true);
  }, [auth.issue]);

  const close = () => {
    setOpen(false);
    setToken("");
    setValidationError(null);
  };

  const save = () => {
    try {
      saveBearerToken(token);
      close();
    } catch (error) {
      setValidationError(error instanceof Error ? error.message : String(error));
    }
  };

  const clear = () => {
    clearBearerToken();
    close();
  };

  const label = auth.issue
    ? auth.issue.kind === "scope"
      ? "Token scope"
      : "Token required"
    : auth.hasToken
      ? "Session token"
      : "API token";

  return (
    <>
      {auth.issue ? <Pill tone="danger">Auth attention</Pill> : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        aria-label={`${label}. Open bearer token settings.`}
      >
        {auth.issue ? (
          <ShieldAlert aria-hidden="true" />
        ) : auth.hasToken ? (
          <ShieldCheck aria-hidden="true" />
        ) : (
          <KeyRound aria-hidden="true" />
        )}
        <span className="hidden sm:inline">{label}</span>
      </Button>

      <span className="sr-only" aria-live="polite">
        {auth.issue?.message ??
          (auth.hasToken ? "Session bearer token stored." : "No bearer token stored.")}
      </span>

      <FormDialog
        open={open}
        onClose={close}
        title="HTTP bearer token"
        description="Authorize this browser tab to call protected Tesseract routes."
        onSubmit={save}
        submitLabel={auth.hasToken ? "Replace token" : "Use token"}
        submitDisabled={!token.trim()}
        widthClassName="w-[34rem] max-w-[calc(100vw-2rem)]"
      >
        <div className="space-y-4">
          {auth.issue ? (
            <Callout tone={auth.issue.kind === "scope" ? "warning" : "danger"}>
              {auth.issue.message}
            </Callout>
          ) : null}

          <div className="space-y-2">
            <Label htmlFor="http-bearer-token">
              {auth.hasToken ? "Replacement bearer token" : "Bearer token"}
            </Label>
            <Input
              id="http-bearer-token"
              type="password"
              value={token}
              onChange={(event) => {
                setToken(event.target.value);
                setValidationError(null);
              }}
              placeholder={auth.hasToken ? "Enter a replacement token" : "Enter a token"}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              aria-describedby="http-bearer-token-help"
              aria-invalid={validationError ? true : undefined}
            />
            <p id="http-bearer-token-help" className="text-xs leading-5 text-text-subtle">
              The token is stored only in this tab&apos;s session storage and sent in the
              Authorization header. It is never placed in a URL. Closing the tab clears it.
            </p>
            {validationError ? (
              <p className="text-xs text-danger" role="alert">
                {validationError}
              </p>
            ) : null}
          </div>

          {auth.storage === "memory" ? (
            <Callout tone="warning">
              Session storage is unavailable, so the token will be kept only in this page until it
              reloads.
            </Callout>
          ) : null}

          <Callout tone="warning">
            Bearer tokens do not encrypt traffic. For remote access, use TLS or a trusted VPN, SSH
            tunnel, or TLS-terminating reverse proxy. Static tokens cannot authorize admin settings
            or configuration changes; those require a managed token with the admin scope.
          </Callout>

          {auth.hasToken ? (
            <div className="flex items-center justify-between gap-4 border-t border-border pt-4">
              <p className="text-xs text-text-subtle">A token is currently stored for this tab.</p>
              <Button type="button" variant="destructive" size="sm" onClick={clear}>
                <Trash2 aria-hidden="true" />
                Clear token
              </Button>
            </div>
          ) : null}
        </div>
      </FormDialog>
    </>
  );
}
