import {
  Button,
  Callout,
  EmptyState,
  Input,
  Label,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SummaryCards,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { ArrowRight, Calculator, FileText, Play, Save, Trash2, Upload } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { estimate, evaluateView } from "../api/client";
import type { EstimateResponse, EvaluationMeta, Record, Selector } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  onOpenRecord?: (namespace: string, key: string) => void;
}

interface Preset {
  name: string;
  selector: Selector;
}

const STORAGE_KEY = "tesseract:viewbuilder:presets";

function loadPresets(): Preset[] {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "[]");
  } catch {
    return [];
  }
}

function savePresets(presets: Preset[]) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(presets));
}

export function ViewBuilderPage({ onOpenRecord }: Props) {
  const [namespaces, setNamespaces] = useState("");
  const [keys, setKeys] = useState("");
  const [revisionScope, setRevisionScope] = useState<"head" | "all">("head");
  const [order, setOrder] = useState("namespace,key,revision");
  const [limit, setLimit] = useState("50");
  const [tagsAny, setTagsAny] = useState("");
  const [results, setResults] = useState<Record[] | null>(null);
  const [evalMeta, setEvalMeta] = useState<EvaluationMeta | null>(null);
  const [estimateResult, setEstimateResult] = useState<EstimateResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [estimating, setEstimating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [presets, setPresetsState] = useState<Preset[]>(loadPresets);
  const [presetName, setPresetName] = useState("");

  const buildSelector = (): Selector => {
    const selector: Selector = {
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
    const orderList = order.trim();
    if (orderList) {
      selector.order = orderList
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean);
    }
    const tagList = tagsAny.trim();
    if (tagList) {
      selector.tags_any = tagList
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

  const handleEvaluate = async () => {
    setLoading(true);
    setError(null);
    setEstimateResult(null);
    try {
      const response = await evaluateView(buildSelector(), true);
      setResults(response.items);
      setEvalMeta(response.evaluation_meta);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const handleSavePreset = () => {
    const name = presetName.trim();
    if (!name) return;
    const updated = [
      ...presets.filter((preset) => preset.name !== name),
      { name, selector: buildSelector() },
    ];
    setPresetsState(updated);
    savePresets(updated);
    setPresetName("");
    toast.success(`Preset "${name}" saved`);
  };

  const handleLoadPreset = (preset: Preset) => {
    const selector = preset.selector;
    setNamespaces(selector.namespaces?.join(", ") ?? "");
    setKeys(selector.keys?.join(", ") ?? "");
    setRevisionScope(selector.revision_scope ?? "head");
    setOrder(selector.order?.join(", ") ?? "namespace,key,revision");
    setLimit(String(selector.limit ?? 50));
    setTagsAny(selector.tags_any?.join(", ") ?? "");
    toast.success(`Loaded preset "${preset.name}"`);
  };

  const handleDeletePreset = (name: string) => {
    const updated = presets.filter((preset) => preset.name !== name);
    setPresetsState(updated);
    savePresets(updated);
  };

  const openRecord = (record: Record) => {
    onOpenRecord?.(record.namespace, record.key);
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-bg text-text">
      <div className="min-h-0 flex-1 overflow-auto">
        <section className="border-b border-border-strong px-4 py-4 lg:px-6">
          <h2 className="text-base font-semibold">Build a reusable record list</h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-text-soft">
            Define and test selectors to see which records match, then save useful selectors as
            browser-local presets.
          </p>
        </section>

        <div className="grid min-h-0 lg:grid-cols-[minmax(0,1fr)_18rem]">
          <main className="min-w-0">
            <section className="border-b border-border-strong bg-panel">
              <div className="grid border-b border-border md:grid-cols-2">
                <div className="px-4 py-3 md:border-r md:border-border lg:px-6">
                  <p className="text-xs font-medium text-text-muted">When to use it</p>
                  <p className="mt-1 text-xs leading-5 text-text-soft">
                    Browse and validate selectors before you package or automate anything.
                  </p>
                </div>
                <div className="border-t border-border px-4 py-3 md:border-t-0 lg:px-6">
                  <p className="text-xs font-medium text-text-muted">Estimate or evaluate</p>
                  <p className="mt-1 text-xs leading-5 text-text-soft">
                    Estimate gives size. Evaluate shows the matching records.
                  </p>
                </div>
              </div>

              <form
                className="space-y-4 px-4 py-5 lg:px-6"
                onSubmit={(event) => {
                  event.preventDefault();
                  void handleEvaluate();
                }}
              >
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-1.5 sm:col-span-2">
                    <Label htmlFor="view-namespaces">Namespaces (comma-separated globs)</Label>
                    <Input
                      id="view-namespaces"
                      className="font-mono"
                      placeholder="user/memory/*, app/test/*"
                      value={namespaces}
                      onChange={(event) => setNamespaces(event.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5 sm:col-span-2">
                    <Label htmlFor="view-keys">Keys (comma-separated)</Label>
                    <Input
                      id="view-keys"
                      className="font-mono"
                      placeholder="status, config, preferences"
                      value={keys}
                      onChange={(event) => setKeys(event.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label id="view-scope-label">Revision scope</Label>
                    <Select
                      value={revisionScope}
                      onValueChange={(value) => setRevisionScope(value as "head" | "all")}
                    >
                      <SelectTrigger className="w-full" aria-labelledby="view-scope-label">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="head">head (latest only)</SelectItem>
                        <SelectItem value="all">all revisions</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="view-limit">Limit</Label>
                    <Input
                      id="view-limit"
                      className="font-mono"
                      type="number"
                      min={1}
                      max={1000}
                      value={limit}
                      onChange={(event) => setLimit(event.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="view-order">Order</Label>
                    <Input
                      id="view-order"
                      className="font-mono"
                      placeholder="namespace,key,revision"
                      value={order}
                      onChange={(event) => setOrder(event.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="view-tags">Tags (any)</Label>
                    <Input
                      id="view-tags"
                      className="font-mono"
                      placeholder="tag1, tag2"
                      value={tagsAny}
                      onChange={(event) => setTagsAny(event.target.value)}
                    />
                  </div>
                </div>

                <p className="max-w-3xl text-xs leading-5 text-text-subtle">
                  This defines the candidate set. Use Packet Builder when you need a bounded payload
                  for an agent or prompt.
                </p>

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
                    {loading ? <Spinner size={14} /> : <Play aria-hidden="true" />}
                    Evaluate
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
                <Callout tone="danger" title="View request failed">
                  {error}
                </Callout>
              </div>
            ) : null}

            {results ? (
              <section aria-labelledby="view-results-heading">
                <div className="flex min-h-10 flex-wrap items-center gap-2 border-b border-border px-4 py-2 lg:px-6">
                  <h2
                    id="view-results-heading"
                    className="mr-auto text-xs font-semibold text-text-muted"
                  >
                    Matching records
                  </h2>
                  {evalMeta ? (
                    <>
                      <Pill tone="info">{evalMeta.matched_count} matched</Pill>
                      <Pill tone="neutral">{evalMeta.normalized_scope}</Pill>
                      {evalMeta.truncated ? <Pill tone="warning">Truncated</Pill> : null}
                    </>
                  ) : null}
                </div>

                {results.length === 0 ? (
                  <EmptyState
                    variant="no-results"
                    title="No records matched"
                    description="Adjust your selector and try again."
                  />
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Namespace</TableHead>
                        <TableHead>Key</TableHead>
                        <TableHead>Rev</TableHead>
                        <TableHead>Actor</TableHead>
                        <TableHead>Created</TableHead>
                        {onOpenRecord ? (
                          <TableHead className="w-10">
                            <span className="sr-only">Open</span>
                          </TableHead>
                        ) : null}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {results.map((record) => (
                        <TableRow key={`${record.namespace}-${record.key}-${record.revision}`}>
                          <TableCell className="font-mono text-xs text-text-soft">
                            {record.namespace}
                          </TableCell>
                          <TableCell>
                            <span className="flex items-center gap-2">
                              <FileText
                                className="size-3.5 shrink-0 text-status-doing"
                                aria-hidden="true"
                              />
                              {record.key}
                            </span>
                          </TableCell>
                          <TableCell className="font-mono text-xs text-text-subtle">
                            r{record.revision}
                          </TableCell>
                          <TableCell className="text-text-soft">{record.actor}</TableCell>
                          <TableCell className="text-xs text-text-soft">
                            {new Date(record.created_at).toLocaleString()}
                          </TableCell>
                          {onOpenRecord ? (
                            <TableCell>
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon-xs"
                                aria-label={`Open ${record.namespace}/${record.key}, revision ${record.revision}`}
                                onClick={() => openRecord(record)}
                              >
                                <ArrowRight aria-hidden="true" />
                              </Button>
                            </TableCell>
                          ) : null}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </section>
            ) : null}
          </main>

          <aside className="min-w-0 border-t border-border-strong bg-panel lg:border-t-0 lg:border-l">
            <form
              className="border-b border-border-strong px-4 py-4"
              onSubmit={(event) => {
                event.preventDefault();
                handleSavePreset();
              }}
            >
              <Label htmlFor="view-preset-name">Save preset</Label>
              <div className="mt-2 flex gap-2">
                <Input
                  id="view-preset-name"
                  placeholder="Preset name"
                  value={presetName}
                  onChange={(event) => setPresetName(event.target.value)}
                />
                <Button
                  type="submit"
                  variant="outline"
                  size="icon"
                  disabled={!presetName.trim()}
                  aria-label="Save preset"
                >
                  <Save aria-hidden="true" />
                </Button>
              </div>
            </form>

            <section aria-labelledby="saved-presets-heading">
              <div className="flex h-10 items-center border-b border-border px-4">
                <h2 id="saved-presets-heading" className="text-xs font-semibold text-text-muted">
                  Saved presets
                </h2>
                <Pill className="ml-auto">{presets.length}</Pill>
              </div>
              {presets.length === 0 ? (
                <p className="px-4 py-5 text-xs leading-5 text-text-subtle">
                  Saved selectors appear here and stay in this browser.
                </p>
              ) : (
                <ul className="divide-y divide-border">
                  {presets.map((preset) => (
                    <li key={preset.name} className="flex items-center gap-1 px-2 py-1.5">
                      <Button
                        type="button"
                        variant="ghost"
                        className="min-w-0 flex-1 justify-start"
                        onClick={() => handleLoadPreset(preset)}
                      >
                        <Upload className="size-3.5 text-text-subtle" aria-hidden="true" />
                        <span className="truncate font-mono text-xs">{preset.name}</span>
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => handleDeletePreset(preset.name)}
                        aria-label={`Delete preset ${preset.name}`}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </aside>
        </div>
      </div>
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
