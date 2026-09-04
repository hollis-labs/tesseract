import {
  Button,
  Callout,
  Checkbox,
  ConfirmDialog,
  EmptyState,
  Input,
  Label,
  PageHeader,
  Pill,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { ListPageLayout, TabStrip, type TabStripItem } from "@hollis-labs/sysop-ui/layout";
import { Check, Copy, Key, List, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { createToken, listTokens, revokeToken } from "../api/client";
import type { AuthToken, TokenCreateResponse } from "../api/types";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

type Tab = "list" | "create";

const TABS: readonly TabStripItem<Tab>[] = [
  { key: "list", label: "Token list", icon: <List className="size-3.5" aria-hidden="true" /> },
  {
    key: "create",
    label: "Create token",
    icon: <Plus className="size-3.5" aria-hidden="true" />,
  },
];

export function AuthTokensPage() {
  const [tab, setTab] = useState<Tab>("list");

  return (
    <ListPageLayout
      header={<PageHeader title="Auth & Tokens" />}
      tabs={<TabStrip tabs={TABS} value={tab} onChange={setTab} />}
    >
      {tab === "list" ? <TokenList /> : <TokenCreateForm onCreated={() => setTab("list")} />}
    </ListPageLayout>
  );
}

function TokenList() {
  const fetcher = useCallback(() => listTokens(), []);
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);
  const [revoking, setRevoking] = useState<string | null>(null);
  const [confirmRevoke, setConfirmRevoke] = useState<AuthToken | null>(null);
  const tokens = data?.tokens ?? [];

  const handleRevoke = async (token: AuthToken) => {
    setRevoking(token.id);
    try {
      await revokeToken(token.id);
      toast.success(`Token "${token.name}" revoked`);
      refresh();
    } catch (reason) {
      toast.error(`Revoke failed: ${reason instanceof Error ? reason.message : reason}`);
    } finally {
      setRevoking(null);
      setConfirmRevoke(null);
    }
  };

  return (
    <>
      <div className="flex items-center justify-between border-b border-border-strong bg-panel px-4 py-3 lg:px-6">
        <div>
          <h2 className="text-sm font-semibold text-text">Managed tokens</h2>
          <p className="mt-1 text-xs text-text-subtle">
            Review client access, namespace reach, and expiration state.
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={refresh} disabled={loading}>
          {loading ? <Spinner size={13} /> : <RefreshCw aria-hidden="true" />}
          Refresh
        </Button>
      </div>

      {error ? (
        <div className="border-b border-border-strong px-4 py-4 lg:px-6">
          <Callout tone="danger" title="Tokens unavailable">
            {error.message}
          </Callout>
        </div>
      ) : null}

      {loading && !data ? (
        <div
          className="flex min-h-48 items-center justify-center"
          role="status"
          aria-label="Loading tokens"
        >
          <Spinner size={20} />
        </div>
      ) : null}

      {!loading && tokens.length === 0 ? (
        <EmptyState
          variant="empty"
          title="No tokens"
          description="Create a managed token to grant scoped client access."
        />
      ) : null}

      {tokens.length > 0 ? (
        <div className="min-w-[70rem]">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Client ID</TableHead>
                <TableHead>Scopes</TableHead>
                <TableHead>Namespaces</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Expires</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((token) => {
                const expired = new Date(token.expires_at) < new Date();
                return (
                  <TableRow key={token.id} className="hover:bg-panel-hover-soft">
                    <TableCell>
                      <span className="flex items-center gap-2 text-sm text-text">
                        <Key className="size-3.5 text-text-muted" aria-hidden="true" />
                        {token.name}
                      </span>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-text-soft">
                      {token.client_id}
                    </TableCell>
                    <TableCell>
                      <div className="flex max-w-72 flex-wrap gap-1">
                        {token.scopes.map((scope) => (
                          <Pill key={scope} tone="info">
                            {scope}
                          </Pill>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="max-w-48 truncate font-mono text-xs text-text-soft">
                      {token.namespace_globs.join(", ")}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-text-subtle">
                      {new Date(token.created_at).toLocaleDateString()}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-text-subtle">
                      {new Date(token.expires_at).toLocaleDateString()}
                    </TableCell>
                    <TableCell>
                      {token.revoked ? (
                        <Pill tone="danger" dot>
                          Revoked
                        </Pill>
                      ) : expired ? (
                        <Pill tone="warning" dot>
                          Expired
                        </Pill>
                      ) : (
                        <Pill tone="success" dot>
                          Active
                        </Pill>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      {!token.revoked ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="xs"
                          onClick={() => setConfirmRevoke(token)}
                          disabled={revoking === token.id}
                          aria-label={`Revoke token ${token.name}`}
                        >
                          {revoking === token.id ? (
                            <Spinner size={11} />
                          ) : (
                            <Trash2 aria-hidden="true" />
                          )}
                          Revoke
                        </Button>
                      ) : null}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      ) : null}

      <ConfirmDialog
        open={confirmRevoke !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmRevoke(null);
        }}
        title="Revoke token"
        description={
          confirmRevoke
            ? `Revoke "${confirmRevoke.name}"? This token will immediately stop authorizing requests.`
            : undefined
        }
        confirmLabel="Revoke"
        busy={confirmRevoke ? revoking === confirmRevoke.id : false}
        onConfirm={() => (confirmRevoke ? handleRevoke(confirmRevoke) : undefined)}
      />
    </>
  );
}

interface TokenCreateFormProps {
  onCreated: () => void;
}

const AVAILABLE_SCOPES = [
  "read",
  "write",
  "promote.request",
  "promote.approve",
  "promote.apply",
  "admin",
];

function TokenCreateForm({ onCreated }: TokenCreateFormProps) {
  const [name, setName] = useState("");
  const [clientId, setClientId] = useState("");
  const [scopes, setScopes] = useState<string[]>(["read"]);
  const [nsGlobs, setNsGlobs] = useState("");
  const [ttl, setTtl] = useState("720h");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TokenCreateResponse | null>(null);
  const [copied, setCopied] = useState(false);

  const canSubmit = Boolean(
    name.trim() && clientId.trim() && scopes.length > 0 && !submitting && !result,
  );

  const toggleScope = (scope: string) => {
    setScopes((previous) =>
      previous.includes(scope) ? previous.filter((item) => item !== scope) : [...previous, scope],
    );
  };

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const request: Parameters<typeof createToken>[0] = {
        name: name.trim(),
        client_id: clientId.trim(),
        scopes,
        namespace_globs: nsGlobs.trim()
          ? nsGlobs
              .split(",")
              .map((value) => value.trim())
              .filter(Boolean)
          : ["*"],
      };
      if (ttl.trim()) request.ttl = ttl.trim();
      const response = await createToken(request);
      setResult(response);
      toast.success("Token created — copy the value now!");
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Create failed: ${message}`);
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopy = async () => {
    if (!result) return;
    await navigator.clipboard.writeText(result.token);
    setCopied(true);
    toast.success("Token copied");
    setTimeout(() => setCopied(false), 2_000);
  };

  return (
    <section className="bg-panel px-4 py-5 lg:px-6" aria-labelledby="create-token-heading">
      <div className="max-w-3xl">
        <div className="mb-5">
          <h2 id="create-token-heading" className="text-sm font-semibold text-text">
            Create a managed token
          </h2>
          <p className="mt-1 text-xs leading-5 text-text-subtle">
            Grant only the scopes and namespaces this client needs. The token value is shown once.
          </p>
        </div>

        {error ? (
          <Callout className="mb-5" tone="danger" title="Token creation failed">
            {error}
          </Callout>
        ) : null}

        {result ? (
          <div className="space-y-4">
            <Callout tone="success" title="Token created">
              Copy this value now. It will not be shown again.
            </Callout>
            <div className="flex items-start gap-2 border border-border bg-bg p-3">
              <code className="min-w-0 flex-1 break-all font-mono text-xs leading-5 text-text">
                {result.token}
              </code>
              <Button
                type="button"
                variant="outline"
                size="icon-sm"
                onClick={handleCopy}
                aria-label={copied ? "Token copied" : "Copy token"}
              >
                {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
              </Button>
            </div>
            <Button type="button" onClick={onCreated}>
              Done — go to token list
            </Button>
          </div>
        ) : (
          <form
            className="space-y-5"
            onSubmit={(event) => {
              event.preventDefault();
              void handleSubmit();
            }}
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="token-name">
                  Name <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="token-name"
                  placeholder="my-agent-token"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="token-client-id">
                  Client ID <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="token-client-id"
                  className="font-mono"
                  placeholder="app:my-agent"
                  value={clientId}
                  onChange={(event) => setClientId(event.target.value)}
                  required
                />
              </div>
            </div>

            <fieldset>
              <legend className="text-xs font-medium text-text-muted">
                Scopes <span className="text-destructive">*</span>
              </legend>
              <div className="mt-2 grid overflow-hidden border border-border sm:grid-cols-2 lg:grid-cols-3">
                {AVAILABLE_SCOPES.map((scope) => (
                  <Label
                    key={scope}
                    htmlFor={`token-scope-${scope}`}
                    className="flex cursor-pointer items-center gap-2 border-b border-border px-3 py-2.5 text-xs text-text-soft last:border-b-0 sm:border-r sm:[&:nth-last-child(-n+2)]:border-b-0 lg:[&:nth-child(3n)]:border-r-0 lg:[&:nth-last-child(-n+3)]:border-b-0"
                  >
                    <Checkbox
                      id={`token-scope-${scope}`}
                      checked={scopes.includes(scope)}
                      onCheckedChange={() => toggleScope(scope)}
                    />
                    <span className="font-mono">{scope}</span>
                  </Label>
                ))}
              </div>
            </fieldset>

            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="token-namespace-globs">
                  Namespace globs{" "}
                  <span className="font-normal text-text-subtle">(comma-separated)</span>
                </Label>
                <Input
                  id="token-namespace-globs"
                  className="font-mono"
                  placeholder="* (all namespaces)"
                  value={nsGlobs}
                  onChange={(event) => setNsGlobs(event.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="token-ttl">TTL</Label>
                <Input
                  id="token-ttl"
                  className="font-mono"
                  placeholder="720h"
                  value={ttl}
                  onChange={(event) => setTtl(event.target.value)}
                />
              </div>
            </div>

            <div className="border-t border-border pt-4">
              <Button type="submit" disabled={!canSubmit}>
                {submitting ? <Spinner size={13} /> : <Key aria-hidden="true" />}
                Create token
              </Button>
            </div>
          </form>
        )}
      </div>
    </section>
  );
}
