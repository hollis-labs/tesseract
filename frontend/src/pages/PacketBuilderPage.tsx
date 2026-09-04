import {
  Button,
  Callout,
  Checkbox,
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
  SummaryCards,
} from "@hollis-labs/sysop-ui";
import { Calculator, ChevronDown, ChevronRight, FileText, Package } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { buildPacket, estimate } from "../api/client";
import type { EstimateResponse, PacketManifest, Record } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  onOpenRecord?: (namespace: string, key: string) => void;
}

export function PacketBuilderPage({ onOpenRecord }: Props) {
  const [namespaces, setNamespaces] = useState("");
  const [keys, setKeys] = useState("");
  const [revisionScope, setRevisionScope] = useState<"head" | "all">("head");
  const [limit] = useState("50");
  const [includePins, setIncludePins] = useState(true);
  const [maxItems, setMaxItems] = useState("50");
  const [maxBytes, setMaxBytes] = useState("");
  const [maxTokens, setMaxTokens] = useState("8000");
  const [payloadMode, setPayloadMode] = useState<"full" | "head_only">("full");
  const [items, setItems] = useState<Record[] | null>(null);
  const [manifest, setManifest] = useState<PacketManifest | null>(null);
  const [estimateResult, setEstimateResult] = useState<EstimateResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [estimating, setEstimating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedItem, setExpandedItem] = useState<number | null>(null);

  const buildSelector = (): import("../api/types").Selector => {
    const selector: import("../api/types").Selector = {
      revision_scope: revisionScope,
      limit: parseInt(limit, 10) || 50,
    };
    const namespaceList = namespaces.trim();
    if (namespaceList) {
      selector.namespaces = namespaceList
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean);
    }
    const keyList = keys.trim();
    if (keyList) {
      selector.keys = keyList
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean);
    }
    return selector;
  };

  const handleEstimate = async () => {
    setEstimating(true);
    setError(null);
    try {
      const response = await estimate(buildSelector());
      setEstimateResult(response);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setEstimating(false);
    }
  };

  const handleBuild = async () => {
    setLoading(true);
    setError(null);
    setEstimateResult(null);
    try {
      const request: Parameters<typeof buildPacket>[0] = {
        selector: buildSelector(),
        include_pins: includePins,
        payload_mode: payloadMode,
      };
      const itemBudget = parseInt(maxItems, 10);
      if (Number.isFinite(itemBudget) && itemBudget > 0) request.max_items = itemBudget;
      if (maxBytes.trim()) {
        const byteBudget = parseInt(maxBytes, 10);
        if (Number.isFinite(byteBudget) && byteBudget > 0) request.max_bytes = byteBudget;
      }
      const tokenBudget = parseInt(maxTokens, 10);
      if (Number.isFinite(tokenBudget) && tokenBudget > 0) {
        request.max_tokens_estimate = tokenBudget;
      }
      const response = await buildPacket(request);
      setItems(response.items);
      setManifest(response.manifest);
      toast.success(`Packet built: ${response.items?.length ?? 0} items`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      toast.error(`Build failed: ${message}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-bg text-text">
      <div className="min-h-0 flex-1 overflow-auto">
        <section className="border-b border-border-strong px-4 py-4 lg:px-6">
          <h2 className="text-base font-semibold">Turn a selector into a bounded packet</h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-text-soft">
            Start with the records you need, then apply item, byte, and token budgets so the result
            is practical to hand to another system.
          </p>
        </section>

        <section className="border-b border-border-strong bg-panel">
          <div className="grid border-b border-border md:grid-cols-2">
            <div className="px-4 py-3 md:border-r md:border-border lg:px-6">
              <p className="text-xs font-medium text-text-muted">What it does</p>
              <p className="mt-1 text-xs leading-5 text-text-soft">
                Starts from a selector, then trims and assembles the result to fit your budget.
              </p>
            </div>
            <div className="border-t border-border px-4 py-3 md:border-t-0 lg:px-6">
              <p className="text-xs font-medium text-text-muted">Relation to View Builder</p>
              <p className="mt-1 text-xs leading-5 text-text-soft">
                View Builder inspects the set. Packet Builder produces the final payload.
              </p>
            </div>
          </div>

          <form
            className="space-y-5 px-4 py-5 lg:px-6"
            onSubmit={(event) => {
              event.preventDefault();
              void handleBuild();
            }}
          >
            <fieldset>
              <legend className="text-xs font-semibold text-text-muted">Selector</legend>
              <div className="mt-3 grid gap-4 md:grid-cols-2">
                <div className="space-y-1.5 md:col-span-2">
                  <Label htmlFor="packet-namespaces">Namespaces</Label>
                  <Input
                    id="packet-namespaces"
                    className="font-mono"
                    placeholder="user/memory/*, app/test/*"
                    value={namespaces}
                    onChange={(event) => setNamespaces(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="packet-keys">Keys</Label>
                  <Input
                    id="packet-keys"
                    className="font-mono"
                    placeholder="status, config"
                    value={keys}
                    onChange={(event) => setKeys(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label id="packet-scope-label">Revision scope</Label>
                  <Select
                    value={revisionScope}
                    onValueChange={(value) => setRevisionScope(value as "head" | "all")}
                  >
                    <SelectTrigger
                      className="w-full font-mono"
                      aria-labelledby="packet-scope-label"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="head">head</SelectItem>
                      <SelectItem value="all">all</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
            </fieldset>

            <fieldset className="border-t border-border pt-5">
              <legend className="text-xs font-semibold text-text-muted">Budget and assembly</legend>
              <div className="mt-3 grid gap-4 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="packet-max-items">Max items</Label>
                  <Input
                    id="packet-max-items"
                    className="font-mono"
                    type="number"
                    min={1}
                    value={maxItems}
                    onChange={(event) => setMaxItems(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="packet-max-tokens">Max tokens</Label>
                  <Input
                    id="packet-max-tokens"
                    className="font-mono"
                    type="number"
                    min={1}
                    value={maxTokens}
                    onChange={(event) => setMaxTokens(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="packet-max-bytes">
                    Max bytes <span className="font-normal text-text-subtle">(optional)</span>
                  </Label>
                  <Input
                    id="packet-max-bytes"
                    className="font-mono"
                    type="number"
                    min={1}
                    placeholder="No limit"
                    value={maxBytes}
                    onChange={(event) => setMaxBytes(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label id="packet-payload-mode-label">Payload mode</Label>
                  <Select
                    value={payloadMode}
                    onValueChange={(value) => setPayloadMode(value as "full" | "head_only")}
                  >
                    <SelectTrigger
                      className="w-full font-mono"
                      aria-labelledby="packet-payload-mode-label"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="full">full</SelectItem>
                      <SelectItem value="head_only">head_only</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>

              <div className="mt-4 flex items-center gap-2">
                <Checkbox
                  id="packet-include-pins"
                  checked={includePins}
                  onCheckedChange={(checked) => setIncludePins(checked)}
                />
                <Label htmlFor="packet-include-pins" className="cursor-pointer font-normal">
                  Include pins
                </Label>
              </div>
              <p className="mt-3 max-w-4xl text-xs leading-5 text-text-subtle">
                Include pins adds pinned records. Payload mode chooses full bodies or record heads.
                A truncated result was intentionally cut to stay within budget.
              </p>
            </fieldset>

            <div className="flex flex-wrap gap-2 border-t border-border pt-4">
              <Button
                type="button"
                variant="outline"
                onClick={() => void handleEstimate()}
                disabled={estimating}
              >
                {estimating ? <Spinner size={14} /> : <Calculator aria-hidden="true" />}
                Estimate
              </Button>
              <Button type="submit" disabled={loading}>
                {loading ? <Spinner size={14} /> : <Package aria-hidden="true" />}
                Build packet
              </Button>
            </div>
          </form>
        </section>

        {estimateResult ? (
          <SummaryCards
            cards={[
              { label: "Records", value: estimateResult.record_count },
              { label: "Bytes", value: formatBytes(estimateResult.total_bytes) },
              { label: "Tokens (est)", value: estimateResult.token_estimate.toLocaleString() },
            ]}
          />
        ) : null}

        {error ? (
          <div className="border-b border-border-strong px-4 py-3 lg:px-6">
            <Callout tone="danger" title="Packet request failed">
              {error}
            </Callout>
          </div>
        ) : null}

        {manifest ? (
          <SummaryCards
            cards={[
              { label: "Items", value: manifest.items_total },
              { label: "Pins", value: manifest.pins_included },
              { label: "Bytes", value: formatBytes(manifest.bytes) },
              { label: "Tokens", value: manifest.tokens_estimate.toLocaleString() },
              ...(manifest.truncated
                ? [
                    {
                      label: "Truncated",
                      value: "Yes",
                      accentColor: "var(--color-status-paused)",
                    },
                  ]
                : []),
            ]}
          />
        ) : null}

        {items ? (
          <section aria-labelledby="packet-results-heading">
            <div className="flex h-10 items-center justify-between border-b border-border px-4 lg:px-6">
              <h2 id="packet-results-heading" className="text-xs font-semibold text-text-muted">
                Packet contents
              </h2>
              <Pill tone={items.length > 0 ? "info" : "neutral"}>
                {items.length} item{items.length === 1 ? "" : "s"}
              </Pill>
            </div>

            {items.length === 0 ? (
              <EmptyState
                variant="empty"
                title="Packet is empty"
                description="No records matched the selector."
              />
            ) : (
              <div className="divide-y divide-border">
                {items.map((item, index) => {
                  const expanded = expandedItem === index;
                  const detailID = `packet-item-${index}`;
                  return (
                    <article key={`${item.namespace}-${item.key}-${item.revision}`}>
                      <div className="flex min-w-0 items-center gap-2 px-2 py-1.5 hover:bg-panel-hover-soft sm:px-4 lg:px-6">
                        <button
                          type="button"
                          className="flex min-w-0 flex-1 items-center gap-2 rounded px-2 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          aria-expanded={expanded}
                          aria-controls={detailID}
                          onClick={() => setExpandedItem(expanded ? null : index)}
                        >
                          {expanded ? (
                            <ChevronDown
                              className="size-4 shrink-0 text-text-subtle"
                              aria-hidden="true"
                            />
                          ) : (
                            <ChevronRight
                              className="size-4 shrink-0 text-text-subtle"
                              aria-hidden="true"
                            />
                          )}
                          <FileText
                            className="size-4 shrink-0 text-status-doing"
                            aria-hidden="true"
                          />
                          <span className="min-w-0 truncate font-mono text-xs text-text-soft">
                            {item.namespace}
                          </span>
                          <span className="min-w-0 truncate text-sm text-text">{item.key}</span>
                          <span className="ml-auto shrink-0 font-mono text-xs text-text-subtle">
                            r{item.revision}
                          </span>
                        </button>
                        {onOpenRecord ? (
                          <Button
                            type="button"
                            variant="ghost"
                            size="xs"
                            onClick={() => onOpenRecord(item.namespace, item.key)}
                          >
                            Open
                          </Button>
                        ) : null}
                      </div>
                      {expanded && item.payload != null ? (
                        <div
                          id={detailID}
                          className="border-t border-border bg-panel-2 px-4 py-3 lg:px-8"
                        >
                          <JsonViewer value={normalizeJson(item.payload)} className="max-h-52" />
                        </div>
                      ) : null}
                    </article>
                  );
                })}
              </div>
            )}
          </section>
        ) : null}
      </div>
    </div>
  );
}

function normalizeJson(data: unknown): unknown {
  if (typeof data === "string") {
    try {
      return JSON.parse(data);
    } catch {
      return data;
    }
  }
  return data ?? null;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
