import {
  Button,
  EmptyState,
  Input,
  Label,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@hollis-labs/sysop-ui";
import { type ColumnDef, DataTable } from "@hollis-labs/sysop-ui/data";
import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react";
import { type RefObject, useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { listNamespaces } from "../../api/client";
import type {
  NamespaceListItem,
  NamespaceMatchMode,
  NamespaceSortDir,
  NamespaceSortField,
} from "../../api/types";

const PAGE_SIZES = [25, 50, 100, 250] as const;

interface Props {
  columns: ColumnDef<NamespaceListItem>[];
  scrollRootRef?: RefObject<HTMLElement | null>;
  /**
   * Bumped by the parent after a register or policy edit so the current page
   * re-queries rather than showing the pre-write registry.
   */
  refreshToken?: number;
}

interface RegistryQuery {
  match: string;
  matchMode: NamespaceMatchMode;
  ownerType: string;
  ownerId: string;
  sort: NamespaceSortField;
  dir: NamespaceSortDir;
  pageSize: number;
}

const INITIAL_QUERY: RegistryQuery = {
  match: "",
  matchMode: "contains",
  ownerType: "",
  ownerId: "",
  sort: "namespace",
  dir: "asc",
  pageSize: 50,
};

/**
 * The namespace registry, filtered, sorted and paged BY THE SERVER.
 *
 * The registry outgrew every surface's response cap (1125 rows against a cap
 * of 1000), so a table built from one capped request showed most of the
 * registry and looked like all of it. Narrowing and ordering happen in SQL
 * here, and the footer always states which slice of which total is on screen
 * (CW-20260909-0003).
 */
export function NamespaceRegistryTable({ columns, scrollRootRef, refreshToken = 0 }: Props) {
  const [query, setQuery] = useState<RegistryQuery>(INITIAL_QUERY);

  const [items, setItems] = useState<NamespaceListItem[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // The cursor stack is what makes "Previous" work over a keyset-paged API:
  // entry N is the cursor that produced page N, so going back is popping it
  // rather than counting rows backwards.
  const [cursorStack, setCursorStack] = useState<(string | undefined)[]>([undefined]);
  const [pageIndex, setPageIndex] = useState(0);
  const [nextCursor, setNextCursor] = useState<string | undefined>();

  // Changing a filter or the ordering invalidates every cursor already issued,
  // because a cursor is bound to the ordering it was minted under. Resetting
  // here rather than in an effect keeps that a consequence of the edit, so
  // there is no render where a stale cursor and the new ordering coexist.
  const updateQuery = useCallback((patch: Partial<RegistryQuery>) => {
    setQuery((current) => ({ ...current, ...patch }));
    setCursorStack([undefined]);
    setPageIndex(0);
  }, []);

  const cursor = cursorStack[pageIndex];
  const { match, matchMode, ownerType, ownerId, sort, dir, pageSize } = query;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await listNamespaces({
        ...(match ? { match, match_mode: matchMode } : {}),
        ...(ownerType ? { owner_type: ownerType } : {}),
        ...(ownerId ? { owner_id: ownerId } : {}),
        sort,
        dir,
        limit: pageSize,
        ...(cursor ? { cursor } : {}),
      });
      setItems(response.items);
      setTotal(response.count);
      setNextCursor(response.next_cursor);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      setItems([]);
      setTotal(0);
      setNextCursor(undefined);
      toast.error(`Namespace list failed: ${message}`);
    } finally {
      setLoading(false);
    }
  }, [match, matchMode, ownerType, ownerId, sort, dir, pageSize, cursor]);

  // refreshToken is a signal, not a value this effect reads. The parent bumps
  // it after a register or policy edit so the current page re-queries; dropped
  // as an "extra" dependency, the table would keep showing the pre-write
  // registry until the filter happened to change.
  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshToken is a re-run signal, not a read value
  useEffect(() => {
    void load();
  }, [load, refreshToken]);

  const goNext = () => {
    if (!nextCursor) return;
    setCursorStack((stack) => {
      const next = stack.slice(0, pageIndex + 1);
      next.push(nextCursor);
      return next;
    });
    setPageIndex((index) => index + 1);
  };

  const goPrevious = () => {
    if (pageIndex === 0) return;
    setPageIndex((index) => index - 1);
  };

  const firstRow = total === 0 ? 0 : pageIndex * pageSize + 1;
  const lastRow = pageIndex * pageSize + items.length;

  return (
    <div className="grid gap-3">
      <div className="grid gap-3 rounded-md border border-border px-4 py-3 md:grid-cols-[minmax(0,2fr)_9rem_minmax(0,1fr)_minmax(0,1fr)]">
        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-match">Filter</Label>
          <Input
            className="font-mono text-xs md:text-xs"
            id="ns-registry-match"
            value={match}
            onChange={(event) => updateQuery({ match: event.target.value })}
            placeholder={matchMode === "glob" ? "user/*/memory/*" : "user/chrispian/"}
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-mode">Match</Label>
          <Select
            value={matchMode}
            onValueChange={(value) => updateQuery({ matchMode: value as NamespaceMatchMode })}
          >
            <SelectTrigger id="ns-registry-mode">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="contains">contains</SelectItem>
              <SelectItem value="prefix">prefix</SelectItem>
              <SelectItem value="glob">glob</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-owner-type">Owner type</Label>
          <Input
            className="font-mono text-xs md:text-xs"
            id="ns-registry-owner-type"
            value={ownerType}
            onChange={(event) => updateQuery({ ownerType: event.target.value })}
            placeholder="any"
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-owner-id">Owner id</Label>
          <Input
            className="font-mono text-xs md:text-xs"
            id="ns-registry-owner-id"
            value={ownerId}
            onChange={(event) => updateQuery({ ownerId: event.target.value })}
            placeholder="any"
          />
        </div>

        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-sort">Sort</Label>
          <Select
            value={sort}
            onValueChange={(value) => updateQuery({ sort: value as NamespaceSortField })}
          >
            <SelectTrigger id="ns-registry-sort">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="namespace">namespace</SelectItem>
              <SelectItem value="owner">owner</SelectItem>
              <SelectItem value="updated_at">updated</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-dir">Direction</Label>
          <Select
            value={dir}
            onValueChange={(value) => updateQuery({ dir: value as NamespaceSortDir })}
          >
            <SelectTrigger id="ns-registry-dir">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="asc">ascending</SelectItem>
              <SelectItem value="desc">descending</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="ns-registry-page-size">Page size</Label>
          <Select
            value={String(pageSize)}
            onValueChange={(value) => updateQuery({ pageSize: Number(value) })}
          >
            <SelectTrigger id="ns-registry-page-size">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PAGE_SIZES.map((size) => (
                <SelectItem key={size} value={String(size)}>
                  {size} per page
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex items-end">
          <Button variant="secondary" onClick={() => void load()} disabled={loading}>
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh
          </Button>
        </div>
      </div>

      <DataTable
        items={items}
        columns={columns}
        getRowId={(row) => row.namespace}
        {...(scrollRootRef ? { scrollRootRef } : {})}
        emptyState={
          <EmptyState
            variant={error ? "error" : "empty"}
            title={
              loading
                ? "Loading namespaces..."
                : error
                  ? "Namespace list failed"
                  : "No namespaces match"
            }
            description={
              error ?? "Registered namespace policy rows matching the filter will appear here."
            }
          />
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-3 px-1">
        {/* Always states the slice AND the total. A footer that reported only
            its own rows is how a partial registry passed for the whole one. */}
        <Pill tone="neutral">
          {total === 0
            ? "0 namespaces"
            : `${firstRow} to ${lastRow} of ${total} namespace${total === 1 ? "" : "s"}`}
        </Pill>
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            onClick={goPrevious}
            disabled={loading || pageIndex === 0}
            aria-label="Previous page"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
            Previous
          </Button>
          <Button
            variant="secondary"
            onClick={goNext}
            disabled={loading || !nextCursor}
            aria-label="Next page"
          >
            Next
            <ChevronRight className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </div>
  );
}
