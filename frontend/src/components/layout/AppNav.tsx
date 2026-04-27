import {
  BookOpen,
  Brain,
  Database,
  HeartPulse,
  HelpCircle,
  Key,
  Layers,
  LayoutDashboard,
  Lightbulb,
  Package,
  PenSquare,
  ScrollText,
  Search,
  Shield,
  Telescope,
  Wrench,
} from "lucide-react";

export type NavPage =
  | "dashboard"
  | "explorer"
  | "recall"
  | "memoryKnowledgeBrowser"
  | "memoryDetail"
  | "searchResearch"
  | "namespaceDetail"
  | "recordDetail"
  | "keyHistory"
  | "compareRevisions"
  | "viewBuilder"
  | "packetBuilder"
  | "writeRecord"
  | "memoryReview"
  | "memoryWrite"
  | "knowledgeWrite"
  | "promote"
  | "policyManager"
  | "audit"
  | "authTokens"
  | "consistency"
  | "maintenance"
  | "broker"
  | "help";

interface NavItem {
  page: NavPage;
  label: string;
  icon: React.ReactNode;
}

interface NavSection {
  title: string;
  items: NavItem[];
}

const NAV_SECTIONS: NavSection[] = [
  {
    title: "Memory & Knowledge",
    items: [
      { page: "memoryKnowledgeBrowser", label: "Memory & Knowledge", icon: <Database size={15} /> },
      { page: "memoryReview", label: "Review Queue", icon: <Brain size={15} /> },
      { page: "memoryWrite", label: "Memory Write", icon: <PenSquare size={15} /> },
      { page: "knowledgeWrite", label: "Knowledge Write", icon: <BookOpen size={15} /> },
    ],
  },
  {
    title: "Context",
    items: [
      { page: "explorer", label: "Context Explorer", icon: <Search size={15} /> },
      { page: "writeRecord", label: "Context Write", icon: <PenSquare size={15} /> },
    ],
  },
  {
    title: "Search & Recall",
    items: [
      { page: "recall", label: "Recall", icon: <Telescope size={15} /> },
      { page: "searchResearch", label: "Search & Research", icon: <Lightbulb size={15} /> },
      { page: "viewBuilder", label: "View Builder", icon: <Layers size={15} /> },
      { page: "packetBuilder", label: "Packet Builder", icon: <Package size={15} /> },
    ],
  },
  {
    title: "Access & Ops",
    items: [
      { page: "policyManager", label: "Policy Manager", icon: <Shield size={15} /> },
      { page: "broker", label: "Broker", icon: <Brain size={15} /> },
      { page: "authTokens", label: "Auth & Tokens", icon: <Key size={15} /> },
      { page: "audit", label: "Audit & Ops", icon: <ScrollText size={15} /> },
    ],
  },
  {
    title: "System",
    items: [
      { page: "consistency", label: "Consistency", icon: <HeartPulse size={15} /> },
      { page: "maintenance", label: "Maintenance", icon: <Wrench size={15} /> },
      { page: "dashboard", label: "Dashboard", icon: <LayoutDashboard size={15} /> },
    ],
  },
];

// Pages that should highlight a parent nav item
const PAGE_PARENT: Partial<globalThis.Record<NavPage, NavPage>> = {
  namespaceDetail: "explorer",
  recordDetail: "explorer",
  keyHistory: "explorer",
  compareRevisions: "explorer",
  promote: "writeRecord",
  memoryDetail: "memoryKnowledgeBrowser",
};

interface Props {
  current: NavPage;
  onNavigate: (page: NavPage) => void;
}

export function AppNav({ current, onNavigate }: Props) {
  const activePage = PAGE_PARENT[current] ?? current;

  return (
    <nav className="app-sidebar" aria-label="Main navigation">
      {NAV_SECTIONS.map((section) => (
        <div key={section.title} className="nav-section">
          <div className="nav-section-title">{section.title}</div>
          {section.items.map((item) => (
            <button
              key={item.page}
              className={`nav-item ${activePage === item.page ? "active" : ""}`}
              onClick={() => onNavigate(item.page)}
            >
              {item.icon}
              {item.label}
            </button>
          ))}
        </div>
      ))}

      <div style={{ flex: 1 }} />

      <div className="nav-section">
        <button
          className={`nav-item ${current === "help" ? "active" : ""}`}
          onClick={() => onNavigate("help")}
        >
          <HelpCircle size={15} />
          Help
        </button>
      </div>
    </nav>
  );
}
