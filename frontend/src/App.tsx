import { useState, useEffect, useCallback } from 'react';
import { Toaster } from 'sonner';
import { AppHeader } from './components/layout/AppHeader';
import { AppNav } from './components/layout/AppNav';
import { AppFooter } from './components/layout/AppFooter';
import { HelpPage } from './pages/HelpPage';
import { ExplorerPage } from './pages/ExplorerPage';
import { NamespaceDetailPage } from './pages/NamespaceDetailPage';
import { RecordDetailPage } from './pages/RecordDetailPage';
import { KeyHistoryPage } from './pages/KeyHistoryPage';
import { CompareRevisionsPage } from './pages/CompareRevisionsPage';
import { ViewBuilderPage } from './pages/ViewBuilderPage';
import { PacketBuilderPage } from './pages/PacketBuilderPage';
import { WriteRecordPage } from './pages/WriteRecordPage';
import { PromotePage } from './pages/PromotePage';
import { BrokerPage } from './pages/BrokerPage';
import { PolicyManagerPage } from './pages/PolicyManagerPage';
import { AuditPage } from './pages/AuditPage';
import { AuthTokensPage } from './pages/AuthTokensPage';
import { ConsistencyPage } from './pages/ConsistencyPage';
import { MaintenancePage } from './pages/MaintenancePage';
import { DashboardPage } from './pages/DashboardPage';
import { usePoll } from './hooks/usePoll';
import { getHealth } from './api/client';
import type { NavPage } from './components/layout/AppNav';
import type { HealthStatus, BrokerPlanResponse } from './api/types';

const PAGE_TITLES: Record<NavPage, string> = {
  dashboard: 'Dashboard',
  explorer: 'Context Explorer',
  namespaceDetail: 'Namespace Detail',
  recordDetail: 'Record Detail',
  keyHistory: 'Key History',
  compareRevisions: 'Compare Revisions',
  viewBuilder: 'View Builder',
  packetBuilder: 'Packet Builder',
  writeRecord: 'Write Record',
  promote: 'Promote',
  policyManager: 'Policy Manager',
  audit: 'Audit & Ops',
  authTokens: 'Auth & Tokens',
  consistency: 'Consistency',
  maintenance: 'Maintenance',
  broker: 'Broker',
  help: 'Help',
};

// Navigation context for detail pages
interface NavContext {
  namespace?: string;
  key?: string;
  revisionA?: number;
  revisionB?: number;
}

export default function App() {
  const [page, setPage] = useState<NavPage>('dashboard');
  const [ctx, setCtx] = useState<NavContext>({});

  const { data: health } = usePoll<HealthStatus>(getHealth, 10_000);

  // Navigation helpers
  const navigate = useCallback((target: NavPage, update?: Partial<NavContext>) => {
    setPage(target);
    if (update) setCtx(prev => ({ ...prev, ...update }));
  }, []);

  const handleNav = useCallback((target: NavPage) => {
    setPage(target);
    setCtx({});
  }, []);

  // Keyboard shortcuts
  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (document.querySelector('.hud-modal-overlay')) return;
    const tag = (e.target as HTMLElement)?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;

    if (e.key === 'Escape') {
      if (page === 'recordDetail') { setPage('namespaceDetail'); e.preventDefault(); }
      else if (page === 'namespaceDetail') { setPage('explorer'); e.preventDefault(); }
      else if (page === 'keyHistory') { setPage('recordDetail'); e.preventDefault(); }
      else if (page === 'compareRevisions') { setPage('keyHistory'); e.preventDefault(); }
      else if (page === 'promote') { setPage('writeRecord'); e.preventDefault(); }
    }

    if (e.key === 'r' && !e.metaKey && !e.ctrlKey) {
      window.dispatchEvent(new CustomEvent('conduit:refresh'));
    }

    if (e.key === '?' && !e.metaKey && !e.ctrlKey) {
      setPage('help');
      e.preventDefault();
    }
  }, [page]);

  useEffect(() => {
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);

  return (
    <div className="app-shell">
      <AppHeader pageTitle={PAGE_TITLES[page]} health={health} />

      <div className="app-body">
        <AppNav current={page} onNavigate={handleNav} />

        <main className="app-content" id="main-content" role="main">
          {/* ── Sprint 2: Read paths ─────────────────── */}
          {page === 'explorer' && (
            <ExplorerPage
              onOpenNamespace={ns => navigate('namespaceDetail', { namespace: ns })}
              onOpenRecord={(ns, key) => navigate('recordDetail', { namespace: ns, key })}
            />
          )}
          {page === 'namespaceDetail' && ctx.namespace && (
            <NamespaceDetailPage
              namespace={ctx.namespace}
              onBack={() => setPage('explorer')}
              onOpenRecord={(ns, key) => navigate('recordDetail', { namespace: ns, key })}
            />
          )}
          {page === 'recordDetail' && ctx.namespace && ctx.key && (
            <RecordDetailPage
              namespace={ctx.namespace}
              recordKey={ctx.key}
              onBack={() => setPage('namespaceDetail')}
              onOpenHistory={(ns, key) => navigate('keyHistory', { namespace: ns, key })}
            />
          )}

          {page === 'keyHistory' && ctx.namespace && ctx.key && (
            <KeyHistoryPage
              namespace={ctx.namespace}
              recordKey={ctx.key}
              onBack={() => setPage('recordDetail')}
              onCompare={(ns, key, a, b) => navigate('compareRevisions', { namespace: ns, key, revisionA: a, revisionB: b })}
            />
          )}
          {page === 'compareRevisions' && ctx.namespace && ctx.key && ctx.revisionA != null && ctx.revisionB != null && (
            <CompareRevisionsPage
              namespace={ctx.namespace}
              recordKey={ctx.key}
              revisionA={ctx.revisionA}
              revisionB={ctx.revisionB}
              onBack={() => setPage('keyHistory')}
            />
          )}

          {/* ── Dashboard ──────────────────────────── */}
          {page === 'dashboard' && (
            <DashboardPage health={health} onNavigate={handleNav} />
          )}
          {page === 'viewBuilder' && (
            <ViewBuilderPage
              onOpenRecord={(ns, key) => navigate('recordDetail', { namespace: ns, key })}
            />
          )}
          {page === 'packetBuilder' && (
            <PacketBuilderPage
              onOpenRecord={(ns, key) => navigate('recordDetail', { namespace: ns, key })}
            />
          )}
          {page === 'writeRecord' && (
            <WriteRecordPage
              onWritten={(ns, key) => navigate('recordDetail', { namespace: ns, key })}
              onOpenPromote={() => setPage('promote')}
            />
          )}
          {page === 'promote' && (
            <PromotePage onBack={() => setPage('writeRecord')} />
          )}
          {page === 'policyManager' && <PolicyManagerPage />}
          {page === 'audit' && <AuditPage />}
          {page === 'authTokens' && <AuthTokensPage />}
          {page === 'consistency' && <ConsistencyPage />}
          {page === 'maintenance' && <MaintenancePage />}
          {page === 'broker' && (
            <BrokerPage
              onExecutePlan={(_plan: BrokerPlanResponse) => setPage('packetBuilder')}
            />
          )}
          {page === 'help' && (
            <HelpPage />
          )}
        </main>
      </div>

      <AppFooter />

      <Toaster
        position="bottom-right"
        toastOptions={{
          style: {
            background: 'rgb(var(--panel2))',
            border: '1px solid rgb(var(--border))',
            color: 'rgb(var(--text))',
            fontFamily: "'Share Tech Mono', monospace",
            fontSize: '13px',
          },
        }}
        theme="dark"
      />
    </div>
  );
}
