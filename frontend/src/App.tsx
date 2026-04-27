import { useCallback, useEffect, useState } from "react";
import { Toaster } from "sonner";
import { getHealth } from "./api/client";
import type { BrokerPlanResponse, HealthStatus } from "./api/types";
import { AppFooter } from "./components/layout/AppFooter";
import { AppHeader } from "./components/layout/AppHeader";
import type { NavPage } from "./components/layout/AppNav";
import { AppNav } from "./components/layout/AppNav";
import { usePoll } from "./hooks/usePoll";
import { AuditPage } from "./pages/AuditPage";
import { AuthTokensPage } from "./pages/AuthTokensPage";
import { BrokerPage } from "./pages/BrokerPage";
import { CompareRevisionsPage } from "./pages/CompareRevisionsPage";
import { ConsistencyPage } from "./pages/ConsistencyPage";
import { DashboardPage } from "./pages/DashboardPage";
import { ExplorerPage } from "./pages/ExplorerPage";
import { HelpPage } from "./pages/HelpPage";
import { KeyHistoryPage } from "./pages/KeyHistoryPage";
import { MaintenancePage } from "./pages/MaintenancePage";
import { MemoryDetailPage } from "./pages/MemoryDetailPage";
import { MemoryKnowledgeBrowserPage } from "./pages/MemoryKnowledgeBrowserPage";
import { MemoryReviewPage } from "./pages/MemoryReviewPage";
import { MemoryWritePage } from "./pages/MemoryWritePage";
import { KnowledgeWritePage } from "./pages/KnowledgeWritePage";
import { NamespaceDetailPage } from "./pages/NamespaceDetailPage";
import { PacketBuilderPage } from "./pages/PacketBuilderPage";
import { PolicyManagerPage } from "./pages/PolicyManagerPage";
import { PromotePage } from "./pages/PromotePage";
import { RecallPage } from "./pages/RecallPage";
import { RecordDetailPage } from "./pages/RecordDetailPage";
import { SearchResearchPage } from "./pages/SearchResearchPage";
import { ViewBuilderPage } from "./pages/ViewBuilderPage";
import { WriteRecordPage } from "./pages/WriteRecordPage";

const PAGE_TITLES: Record<NavPage, string> = {
  dashboard: "Dashboard",
  explorer: "Context Explorer",
  recall: "Recall",
  memoryKnowledgeBrowser: "Memory & Knowledge Browser",
  memoryDetail: "Memory / Knowledge Revision",
  searchResearch: "Search & Research",
  namespaceDetail: "Namespace Detail",
  recordDetail: "Record Detail",
  keyHistory: "Key History",
  compareRevisions: "Compare Revisions",
  viewBuilder: "View Builder",
  packetBuilder: "Packet Builder",
  writeRecord: "Write Record",
  memoryReview: "Memory Review",
  memoryWrite: "Memory Write",
  knowledgeWrite: "Knowledge Write",
  promote: "Promote",
  policyManager: "Policy Manager",
  audit: "Audit & Ops",
  authTokens: "Auth & Tokens",
  consistency: "Consistency",
  maintenance: "Maintenance",
  broker: "Broker",
  help: "Help",
};

// Navigation context for detail pages
interface NavContext {
  namespace?: string;
  key?: string;
  revisionA?: number;
  revisionB?: number;
  // For memory/knowledge detail navigation: which domain handler to use.
  domain?: "memory" | "knowledge";
  reviewPreset?: "lowConfidence" | "reviewed" | "pendingReview";
}

function readRouteFromHash(): { page: NavPage; ctx: NavContext } {
  if (typeof window === "undefined") return { page: "dashboard", ctx: {} };
  const raw = window.location.hash.replace(/^#/, "");
  if (!raw) return { page: "dashboard", ctx: {} };

  const [pagePart, queryPart = ""] = raw.split("?");
  const pageCandidate = pagePart as NavPage;
  if (!(pageCandidate in PAGE_TITLES)) {
    return { page: "dashboard", ctx: {} };
  }

  const params = new URLSearchParams(queryPart);
  const ctx: NavContext = {};
  const namespace = params.get("namespace");
  const key = params.get("key");
  const domain = params.get("domain");
  const revisionA = params.get("revisionA");
  const revisionB = params.get("revisionB");
  const reviewPreset = params.get("reviewPreset");

  if (namespace) ctx.namespace = namespace;
  if (key) ctx.key = key;
  if (domain === "memory" || domain === "knowledge") ctx.domain = domain;
  if (revisionA && Number.isFinite(Number(revisionA))) ctx.revisionA = Number(revisionA);
  if (revisionB && Number.isFinite(Number(revisionB))) ctx.revisionB = Number(revisionB);
  if (
    reviewPreset === "lowConfidence" ||
    reviewPreset === "reviewed" ||
    reviewPreset === "pendingReview"
  ) {
    ctx.reviewPreset = reviewPreset;
  }

  return { page: pageCandidate, ctx };
}

function writeRouteToHash(page: NavPage, ctx: NavContext): void {
  if (typeof window === "undefined") return;
  const params = new URLSearchParams();
  if (ctx.namespace) params.set("namespace", ctx.namespace);
  if (ctx.key) params.set("key", ctx.key);
  if (ctx.domain) params.set("domain", ctx.domain);
  if (ctx.revisionA != null) params.set("revisionA", String(ctx.revisionA));
  if (ctx.revisionB != null) params.set("revisionB", String(ctx.revisionB));
  if (ctx.reviewPreset) params.set("reviewPreset", ctx.reviewPreset);
  const qs = params.toString();
  const nextHash = qs ? `#${page}?${qs}` : `#${page}`;
  if (window.location.hash !== nextHash) {
    window.location.hash = nextHash;
  }
}

export default function App() {
  const initialRoute = readRouteFromHash();
  const [page, setPage] = useState<NavPage>(initialRoute.page);
  const [ctx, setCtx] = useState<NavContext>(initialRoute.ctx);

  const { data: health } = usePoll<HealthStatus>(getHealth, 10_000);

  // Navigation helpers
  const navigate = useCallback((target: NavPage, update?: Partial<NavContext>) => {
    setPage(target);
    setCtx(update ?? {});
  }, []);

  const handleNav = useCallback((target: NavPage) => {
    setPage(target);
    setCtx({});
  }, []);

  // Keyboard shortcuts
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (document.querySelector(".hud-modal-overlay")) return;
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;

      if (e.key === "Escape") {
        if (page === "recordDetail") {
          setPage("namespaceDetail");
          e.preventDefault();
        } else if (page === "namespaceDetail") {
          setPage("explorer");
          e.preventDefault();
        } else if (page === "keyHistory") {
          setPage("recordDetail");
          e.preventDefault();
        } else if (page === "compareRevisions") {
          setPage("keyHistory");
          e.preventDefault();
        } else if (page === "promote") {
          setPage("writeRecord");
          e.preventDefault();
        } else if (page === "memoryDetail") {
          setPage("memoryKnowledgeBrowser");
          e.preventDefault();
        }
      }

      if (e.key === "r" && !e.metaKey && !e.ctrlKey) {
        window.dispatchEvent(new CustomEvent("conduit:refresh"));
      }

      if (e.key === "?" && !e.metaKey && !e.ctrlKey) {
        setPage("help");
        e.preventDefault();
      }
    },
    [page],
  );

  useEffect(() => {
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [handleKeyDown]);

  useEffect(() => {
    const onHashChange = () => {
      const route = readRouteFromHash();
      setPage(route.page);
      setCtx(route.ctx);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  useEffect(() => {
    writeRouteToHash(page, ctx);
  }, [page, ctx]);

  return (
    <div className="app-shell">
      <AppHeader pageTitle={PAGE_TITLES[page]} health={health} />

      <div className="app-body">
        <AppNav current={page} onNavigate={handleNav} />

        <main className="app-content" id="main-content" role="main">
          {/* ── Sprint 2: Read paths ─────────────────── */}
          {page === "explorer" && (
            <ExplorerPage
              onOpenNamespace={(ns) => navigate("namespaceDetail", { namespace: ns })}
              onOpenRecord={(ns, key) => navigate("recordDetail", { namespace: ns, key })}
            />
          )}
          {page === "recall" && (
            <RecallPage
              onOpenItem={(d, ns, key) =>
                navigate("memoryDetail", { domain: d, namespace: ns, key })
              }
            />
          )}
          {page === "memoryKnowledgeBrowser" && (
            <MemoryKnowledgeBrowserPage
              onOpenItem={(d, ns, key) =>
                navigate("memoryDetail", { domain: d, namespace: ns, key })
              }
            />
          )}
          {page === "memoryDetail" && ctx.namespace && ctx.key && ctx.domain && (
            <MemoryDetailPage
              domain={ctx.domain}
              namespace={ctx.namespace}
              memoryKey={ctx.key}
              onBack={() => setPage("memoryKnowledgeBrowser")}
            />
          )}
          {page === "searchResearch" && (
            <SearchResearchPage
              onOpenItem={(d, ns, key) =>
                navigate("memoryDetail", { domain: d, namespace: ns, key })
              }
            />
          )}
          {page === "namespaceDetail" && ctx.namespace && (
            <NamespaceDetailPage
              namespace={ctx.namespace}
              onBack={() => setPage("explorer")}
              onOpenRecord={(ns, key) => navigate("recordDetail", { namespace: ns, key })}
            />
          )}
          {page === "recordDetail" && ctx.namespace && ctx.key && (
            <RecordDetailPage
              namespace={ctx.namespace}
              recordKey={ctx.key}
              onBack={() => setPage("namespaceDetail")}
              onOpenHistory={(ns, key) => navigate("keyHistory", { namespace: ns, key })}
            />
          )}

          {page === "keyHistory" && ctx.namespace && ctx.key && (
            <KeyHistoryPage
              namespace={ctx.namespace}
              recordKey={ctx.key}
              onBack={() => setPage("recordDetail")}
              onCompare={(ns, key, a, b) =>
                navigate("compareRevisions", { namespace: ns, key, revisionA: a, revisionB: b })
              }
            />
          )}
          {page === "compareRevisions" &&
            ctx.namespace &&
            ctx.key &&
            ctx.revisionA != null &&
            ctx.revisionB != null && (
              <CompareRevisionsPage
                namespace={ctx.namespace}
                recordKey={ctx.key}
                revisionA={ctx.revisionA}
                revisionB={ctx.revisionB}
                onBack={() => setPage("keyHistory")}
              />
            )}

          {/* ── Dashboard ──────────────────────────── */}
          {page === "dashboard" && <DashboardPage health={health} onNavigate={navigate} />}
          {page === "viewBuilder" && (
            <ViewBuilderPage
              onOpenRecord={(ns, key) => navigate("recordDetail", { namespace: ns, key })}
            />
          )}
          {page === "packetBuilder" && (
            <PacketBuilderPage
              onOpenRecord={(ns, key) => navigate("recordDetail", { namespace: ns, key })}
            />
          )}
          {page === "writeRecord" && (
            <WriteRecordPage
              onWritten={(ns, key) => navigate("recordDetail", { namespace: ns, key })}
              onOpenPromote={() => setPage("promote")}
            />
          )}
          {page === "memoryReview" && (
            <MemoryReviewPage
              onOpenItem={(d, ns, key) =>
                navigate("memoryDetail", { domain: d, namespace: ns, key })
              }
              onOpenWrite={() => navigate("memoryWrite")}
              initialPreset={ctx.reviewPreset}
            />
          )}
          {page === "memoryWrite" && (
            <MemoryWritePage
              onOpenItem={(d, ns, key) =>
                navigate("memoryDetail", { domain: d, namespace: ns, key })
              }
              onOpenReview={() => navigate("memoryReview")}
            />
          )}
          {page === "knowledgeWrite" && (
            <KnowledgeWritePage
              onOpenItem={(d, ns, key) =>
                navigate("memoryDetail", { domain: d, namespace: ns, key })
              }
            />
          )}
          {page === "promote" && <PromotePage onBack={() => setPage("writeRecord")} />}
          {page === "policyManager" && <PolicyManagerPage />}
          {page === "audit" && (
            <AuditPage
              onOpenItem={(domain, ns, key) => {
                if (domain === "context") {
                  navigate("recordDetail", { namespace: ns, key });
                } else {
                  navigate("memoryDetail", { domain, namespace: ns, key });
                }
              }}
            />
          )}
          {page === "authTokens" && <AuthTokensPage />}
          {page === "consistency" && <ConsistencyPage />}
          {page === "maintenance" && <MaintenancePage />}
          {page === "broker" && (
            <BrokerPage onExecutePlan={(_plan: BrokerPlanResponse) => setPage("packetBuilder")} />
          )}
          {page === "help" && <HelpPage />}
        </main>
      </div>

      <AppFooter />

      <Toaster
        position="bottom-right"
        toastOptions={{
          style: {
            background: "rgb(var(--panel2))",
            border: "1px solid rgb(var(--border))",
            color: "rgb(var(--text))",
            fontFamily: "'Share Tech Mono', monospace",
            fontSize: "13px",
          },
        }}
        theme="dark"
      />
    </div>
  );
}
