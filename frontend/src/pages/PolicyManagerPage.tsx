import {
  Button,
  Callout,
  EmptyState,
  Input,
  JsonViewer,
  Label,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SettingsField,
  SettingsGrid,
  SummaryCards,
} from "@hollis-labs/sysop-ui";
import { ListPageLayout, TabStrip, type TabStripItem } from "@hollis-labs/sysop-ui/layout";
import { ChevronDown, ChevronRight, Plus, RefreshCw, Shield } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { evaluateView, getNamespacePolicy, registerNamespace } from "../api/client";
import type { NamespacePolicy } from "../api/types";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

type Tab = "list" | "register";
const TIER_OPTIONS = ["memory", "cache", "pins", "draft", "session"] as const;

const TABS: TabStripItem<Tab>[] = [
  { key: "list", label: "Namespace policies", icon: <Shield className="size-3.5" /> },
  { key: "register", label: "Create or update", icon: <Plus className="size-3.5" /> },
];

export function PolicyManagerPage() {
  const [tab, setTab] = useState<Tab>("list");

  return (
    <ListPageLayout header={null} tabs={<TabStrip tabs={TABS} value={tab} onChange={setTab} />}>
      <section className="border-b border-border-strong px-4 py-4 lg:px-6">
        <h2 className="text-base font-semibold">Register namespace ownership and guardrails</h2>
        <p className="mt-1 max-w-3xl text-sm leading-6 text-text-soft">
          Namespaces can exist without a stored policy. Create or update the explicit owner and
          validation rules here.
        </p>
      </section>

      {tab === "list" ? <PolicyList /> : <RegisterForm onRegistered={() => setTab("list")} />}
    </ListPageLayout>
  );
}

function PolicyList() {
  const fetcher = useCallback(() => evaluateView({ revision_scope: "head", limit: 500 }), []);
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  const namespaces = useMemo(() => {
    if (!data?.items) return [];
    return Array.from(new Set(data.items.map((record) => record.namespace))).sort();
  }, [data]);

  const [policies, setPolicies] = useState<Map<string, NamespacePolicy>>(new Map());
  const [loadingPolicies, setLoadingPolicies] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);

  const loadPolicies = useCallback(async () => {
    if (namespaces.length === 0) {
      setPolicies(new Map());
      return;
    }
    setLoadingPolicies(true);
    const nextPolicies = new Map<string, NamespacePolicy>();
    await Promise.allSettled(
      namespaces.map(async (namespace) => {
        try {
          const policy = await getNamespacePolicy(namespace);
          nextPolicies.set(namespace, policy);
        } catch {
          // A namespace may exist in records without a registered policy.
        }
      }),
    );
    setPolicies(nextPolicies);
    setLoadingPolicies(false);
  }, [namespaces]);

  useEffect(() => {
    void loadPolicies();
  }, [loadPolicies]);

  const unregisteredCount = namespaces.length - policies.size;

  return (
    <>
      <div className="border-b border-border-strong px-4 py-3 lg:px-6">
        <p className="max-w-3xl text-xs leading-5 text-text-soft">
          This list starts from namespaces already seen in records. No policy means the namespace
          exists, but owner and policy metadata have not been registered.
        </p>
      </div>

      <SummaryCards
        cards={[
          { label: "Namespaces", value: namespaces.length },
          { label: "Registered", value: policies.size },
          {
            label: "Without policy",
            value: unregisteredCount,
            accentColor:
              unregisteredCount > 0 ? "var(--color-status-paused)" : "var(--color-status-done)",
          },
        ]}
      />

      <div className="flex min-h-11 items-center justify-end border-b border-border px-4 py-2 lg:px-6">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            refresh();
            void loadPolicies();
          }}
          disabled={loading || loadingPolicies}
        >
          {loading || loadingPolicies ? <Spinner size={14} /> : <RefreshCw aria-hidden="true" />}
          Refresh
        </Button>
      </div>

      {error ? (
        <div className="border-b border-border-strong px-4 py-3 lg:px-6">
          <Callout tone="danger" title="Namespace load failed">
            {error.message}
          </Callout>
        </div>
      ) : null}

      {loading && !data ? (
        <div className="flex justify-center py-12 text-text-subtle">
          <Spinner size={20} />
        </div>
      ) : null}

      {!loading && namespaces.length === 0 ? (
        <EmptyState
          variant="empty"
          title="No namespaces found"
          description="Write a record to create a namespace."
        />
      ) : null}

      {namespaces.length > 0 ? (
        <section className="divide-y divide-border" aria-label="Namespace policies">
          {namespaces.map((namespace) => {
            const policy = policies.get(namespace);
            // Runtime responses may omit the nested policy despite the static API type.
            const values = policy?.policy ?? {};
            const extraKeys = Object.keys(values).filter(
              (key) =>
                ![
                  "tier",
                  "retention",
                  "max_revisions",
                  "max_bytes_per_key",
                  "allowed_ops",
                ].includes(key),
            );
            const isExpanded = expanded === namespace;
            const detailsID = `policy-${toID(namespace)}`;

            return (
              <article key={namespace}>
                <button
                  type="button"
                  className="flex w-full items-center gap-2 px-4 py-3 text-left outline-none transition-colors hover:bg-panel-hover-soft focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring lg:px-6"
                  aria-expanded={isExpanded}
                  aria-controls={detailsID}
                  onClick={() => setExpanded(isExpanded ? null : namespace)}
                >
                  {isExpanded ? (
                    <ChevronDown className="size-4 shrink-0 text-text-subtle" aria-hidden="true" />
                  ) : (
                    <ChevronRight className="size-4 shrink-0 text-text-subtle" aria-hidden="true" />
                  )}
                  <Shield className="size-4 shrink-0 text-status-doing" aria-hidden="true" />
                  <span className="min-w-0 truncate font-mono text-sm text-text">{namespace}</span>
                  <span className="ml-auto flex shrink-0 items-center gap-2">
                    {policy ? (
                      <>
                        {values.tier ? <Pill tone="info">{values.tier}</Pill> : null}
                        {values.retention ? (
                          <span className="hidden text-xs text-text-subtle sm:inline">
                            {values.retention}
                          </span>
                        ) : null}
                      </>
                    ) : (
                      <Pill tone="warning">No policy</Pill>
                    )}
                  </span>
                </button>

                {isExpanded ? (
                  <div id={detailsID} className="border-t border-border bg-panel-2">
                    {policy ? (
                      <>
                        <SettingsGrid className="sm:grid-cols-[10rem_minmax(0,1fr)_10rem_minmax(0,1fr)]">
                          <SettingsField label="Owner">
                            <span className="font-mono">
                              {policy.owner_type}:{policy.owner_id}
                            </span>
                          </SettingsField>
                          <SettingsField label="Tier">{values.tier ?? "—"}</SettingsField>
                          <SettingsField label="Retention">{values.retention ?? "—"}</SettingsField>
                          <SettingsField label="Max revisions">
                            {values.max_revisions ?? "—"}
                          </SettingsField>
                          <SettingsField label="Max bytes/key">
                            {values.max_bytes_per_key ? formatBytes(values.max_bytes_per_key) : "—"}
                          </SettingsField>
                          <SettingsField label="Allowed ops">
                            {values.allowed_ops?.join(", ") ?? "all"}
                          </SettingsField>
                        </SettingsGrid>
                        <div className="border-t border-border px-4 py-3 lg:px-6">
                          <p className="text-xs leading-5 text-text-subtle">
                            Allowed ops, max bytes, and required schema keys affect live validation.
                            Retention and max revisions guide cleanup and compaction.
                          </p>
                        </div>
                        {extraKeys.length > 0 ? (
                          <div className="border-t border-border px-4 py-3 lg:px-6">
                            <h3 className="mb-2 text-xs font-semibold text-text-muted">
                              Full policy
                            </h3>
                            <JsonViewer value={values} className="max-h-40" />
                          </div>
                        ) : null}
                      </>
                    ) : (
                      <p className="px-4 py-4 text-sm text-text-soft lg:px-6">
                        No policy is registered for this namespace. Use Create or update to add one.
                      </p>
                    )}
                  </div>
                ) : null}
              </article>
            );
          })}
        </section>
      ) : null}
    </>
  );
}

interface RegisterFormProps {
  onRegistered: () => void;
}

function RegisterForm({ onRegistered }: RegisterFormProps) {
  const [namespace, setNamespace] = useState("");
  const [ownerType, setOwnerType] = useState("user");
  const [ownerId, setOwnerId] = useState("");
  const [tier, setTier] = useState<(typeof TIER_OPTIONS)[number] | "">("");
  const [retention, setRetention] = useState("");
  const [maxRevisions, setMaxRevisions] = useState("");
  const [maxBytesPerKey, setMaxBytesPerKey] = useState("");
  const [allowedOps, setAllowedOps] = useState("");
  const [requiredSchemaKeys, setRequiredSchemaKeys] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canSubmit = Boolean(namespace.trim() && ownerId.trim() && !submitting);

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const policy: Parameters<typeof registerNamespace>[3] & {
        required_schema_keys?: string[];
      } = {};
      if (tier) policy.tier = tier;
      if (retention.trim()) policy.retention = retention.trim();
      if (maxRevisions.trim()) {
        const count = parseInt(maxRevisions, 10);
        if (Number.isFinite(count) && count > 0) policy.max_revisions = count;
      }
      if (maxBytesPerKey.trim()) {
        const count = parseInt(maxBytesPerKey, 10);
        if (Number.isFinite(count) && count > 0) policy.max_bytes_per_key = count;
      }
      if (allowedOps.trim()) {
        policy.allowed_ops = allowedOps
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean);
      }
      if (requiredSchemaKeys.trim()) {
        policy.required_schema_keys = requiredSchemaKeys
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean);
      }
      await registerNamespace(namespace.trim(), ownerType, ownerId.trim(), policy);
      toast.success(`Namespace "${namespace.trim()}" registered`);
      onRegistered();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      toast.error(`Registration failed: ${message}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form
      className="bg-panel"
      onSubmit={(event) => {
        event.preventDefault();
        void handleSubmit();
      }}
    >
      <div className="border-b border-border-strong px-4 py-3 lg:px-6">
        <p className="max-w-3xl text-xs leading-5 text-text-soft">
          Submitting persists the namespace owner and guardrails. Reusing a namespace updates its
          stored policy.
        </p>
      </div>

      {error ? (
        <div className="border-b border-border-strong px-4 py-3 lg:px-6">
          <Callout tone="danger" title="Policy registration failed">
            {error}
          </Callout>
        </div>
      ) : null}

      <fieldset className="border-b border-border-strong px-4 py-5 lg:px-6">
        <legend className="text-xs font-semibold text-text-muted">Namespace</legend>
        <div className="mt-3 max-w-3xl space-y-1.5">
          <Label htmlFor="policy-namespace">Namespace pattern *</Label>
          <Input
            id="policy-namespace"
            className="font-mono"
            placeholder="app/my-project/*"
            value={namespace}
            onChange={(event) => setNamespace(event.target.value)}
            required
          />
        </div>
      </fieldset>

      <fieldset className="border-b border-border-strong px-4 py-5 lg:px-6">
        <legend className="text-xs font-semibold text-text-muted">Owner</legend>
        <div className="mt-3 grid max-w-3xl gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label id="policy-owner-type-label">Owner type *</Label>
            <Select value={ownerType} onValueChange={(value) => setOwnerType(value ?? "user")}>
              <SelectTrigger className="w-full" aria-labelledby="policy-owner-type-label">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="user">user</SelectItem>
                <SelectItem value="app">app</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="policy-owner-id">Owner ID *</Label>
            <Input
              id="policy-owner-id"
              className="font-mono"
              placeholder="jane"
              value={ownerId}
              onChange={(event) => setOwnerId(event.target.value)}
              required
            />
          </div>
        </div>
      </fieldset>

      <fieldset className="border-b border-border-strong px-4 py-5 lg:px-6">
        <legend className="text-xs font-semibold text-text-muted">Policy</legend>
        <div className="mt-3 grid max-w-3xl gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label id="policy-tier-label">Namespace tier</Label>
            <Select
              value={tier || "unset"}
              onValueChange={(value) =>
                setTier(value === "unset" ? "" : (value as (typeof TIER_OPTIONS)[number]))
              }
            >
              <SelectTrigger className="w-full" aria-labelledby="policy-tier-label">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="unset">infer / leave unset</SelectItem>
                {TIER_OPTIONS.map((option) => (
                  <SelectItem key={option} value={option}>
                    {option}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="policy-retention">Retention</Label>
            <Input
              id="policy-retention"
              className="font-mono"
              placeholder="720h (30 days)"
              value={retention}
              onChange={(event) => setRetention(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="policy-max-revisions">Max revisions</Label>
            <Input
              id="policy-max-revisions"
              className="font-mono"
              type="number"
              min={1}
              placeholder="100"
              value={maxRevisions}
              onChange={(event) => setMaxRevisions(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="policy-max-bytes">Max bytes/key</Label>
            <Input
              id="policy-max-bytes"
              className="font-mono"
              type="number"
              min={1}
              placeholder="1048576"
              value={maxBytesPerKey}
              onChange={(event) => setMaxBytesPerKey(event.target.value)}
            />
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="policy-allowed-ops">
              Allowed ops <span className="font-normal text-text-subtle">(comma-separated)</span>
            </Label>
            <Input
              id="policy-allowed-ops"
              className="font-mono"
              placeholder="read, write, promote"
              value={allowedOps}
              onChange={(event) => setAllowedOps(event.target.value)}
            />
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="policy-schema-keys">
              Required schema keys <span className="font-normal text-text-subtle">(optional)</span>
            </Label>
            <Input
              id="policy-schema-keys"
              className="font-mono"
              placeholder="title, summary, source"
              value={requiredSchemaKeys}
              onChange={(event) => setRequiredSchemaKeys(event.target.value)}
            />
          </div>
        </div>
        <p className="mt-4 max-w-3xl text-xs leading-5 text-text-subtle">
          Use the canonical tiers: memory, cache, pins, draft, or session. Allowed ops and required
          schema keys are active guardrails; retention and max revisions shape maintenance.
        </p>
      </fieldset>

      <div className="px-4 py-4 lg:px-6">
        <Button type="submit" disabled={!canSubmit}>
          {submitting ? <Spinner size={14} /> : <Shield aria-hidden="true" />}
          Save namespace policy
        </Button>
      </div>
    </form>
  );
}

function toID(value: string): string {
  return value.replace(/[^a-zA-Z0-9_-]/g, "-");
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
