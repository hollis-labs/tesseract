import {
  BookOpen,
  Brain,
  Database,
  FilePlus2,
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
  ServerCog,
  Shield,
  Telescope,
  Workflow,
  Wrench,
} from "lucide-react";
import type { ReactNode } from "react";

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
  | "admin"
  | "help";

/** Display titles for every routable page, shown in the kit `PageHeader`. */
export const PAGE_TITLES: Record<NavPage, string> = {
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
  admin: "Admin",
  help: "Help",
};

export interface NavItem {
  page: NavPage;
  /** Tooltip + accessible label on the icon rail. */
  label: string;
  icon: ReactNode;
  /** Pin to the bottom of the rail (e.g. Help). */
  footer?: boolean;
}

const ICON = "h-4 w-4";

/**
 * The icon rail destinations, ordered by the old sidebar's section order
 * (Memory & Knowledge → Context → Search & Recall → Access & Ops → System).
 * The kit `NavRail` is a flat rail, so section headers are dropped; the
 * `label` surfaces as a tooltip instead.
 */
export const NAV_ITEMS: NavItem[] = [
  {
    page: "memoryKnowledgeBrowser",
    label: "Memory & Knowledge",
    icon: <Database className={ICON} />,
  },
  { page: "memoryReview", label: "Review Queue", icon: <Brain className={ICON} /> },
  { page: "memoryWrite", label: "Memory Write", icon: <PenSquare className={ICON} /> },
  { page: "knowledgeWrite", label: "Knowledge Write", icon: <BookOpen className={ICON} /> },
  { page: "explorer", label: "Context Explorer", icon: <Search className={ICON} /> },
  { page: "writeRecord", label: "Context Write", icon: <FilePlus2 className={ICON} /> },
  { page: "recall", label: "Recall", icon: <Telescope className={ICON} /> },
  { page: "searchResearch", label: "Search & Research", icon: <Lightbulb className={ICON} /> },
  { page: "viewBuilder", label: "View Builder", icon: <Layers className={ICON} /> },
  { page: "packetBuilder", label: "Packet Builder", icon: <Package className={ICON} /> },
  { page: "policyManager", label: "Policy Manager", icon: <Shield className={ICON} /> },
  { page: "broker", label: "Broker", icon: <Workflow className={ICON} /> },
  { page: "authTokens", label: "Auth & Tokens", icon: <Key className={ICON} /> },
  { page: "audit", label: "Audit & Ops", icon: <ScrollText className={ICON} /> },
  { page: "consistency", label: "Consistency", icon: <HeartPulse className={ICON} /> },
  { page: "maintenance", label: "Maintenance", icon: <Wrench className={ICON} /> },
  { page: "admin", label: "Admin", icon: <ServerCog className={ICON} /> },
  { page: "dashboard", label: "Dashboard", icon: <LayoutDashboard className={ICON} /> },
  { page: "help", label: "Help", icon: <HelpCircle className={ICON} />, footer: true },
];

/** Detail pages that should highlight a parent rail item. */
export const PAGE_PARENT: Partial<Record<NavPage, NavPage>> = {
  namespaceDetail: "explorer",
  recordDetail: "explorer",
  keyHistory: "explorer",
  compareRevisions: "explorer",
  promote: "writeRecord",
  memoryDetail: "memoryKnowledgeBrowser",
};
