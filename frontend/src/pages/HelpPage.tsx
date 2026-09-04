import {
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { ListPageLayout } from "@hollis-labs/sysop-ui/layout";
import { Keyboard, Monitor, Navigation, Zap } from "lucide-react";
import type { ReactNode } from "react";

const SHORTCUTS: { key: string; description: string }[] = [
  { key: "R", description: "Refresh current view data" },
  { key: "?", description: "Open this help page" },
  { key: "Escape", description: "Navigate back (detail → list)" },
];

const NAV_SECTIONS = [
  {
    title: "Search & Recall",
    items: [
      {
        name: "View Builder",
        description: "Build and save selectors to inspect matching records before packaging them.",
      },
      {
        name: "Packet Builder",
        description:
          "Turn a selector into a bounded context packet with item, byte, and token budgets.",
      },
      {
        name: "Broker",
        description: "Generate a recommended selector and assembly plan from a high-level intent.",
      },
      {
        name: "Recall / Search & Research",
        description:
          "Search memory and knowledge directly when you want answers instead of manual selector building.",
      },
    ],
  },
  {
    title: "Write & Curate",
    items: [
      {
        name: "Memory Review",
        description: "Triage low-confidence, reviewed, deprecated, and promotable memory items.",
      },
      {
        name: "Memory Write / Knowledge Write / Context Write",
        description: "Write domain-specific records without hand-authoring JSON.",
      },
      {
        name: "Policy Manager",
        description:
          "Create or update namespace ownership and guardrails like allowed ops, retention, and schema keys.",
      },
    ],
  },
  {
    title: "Ops & System",
    items: [
      {
        name: "Auth & Tokens",
        description:
          "Create and manage API tokens. Set scopes and namespace globs. Token values shown once on creation.",
      },
      {
        name: "Consistency",
        description: "Scan database for consistency issues. Repair by rebuilding head pointers.",
      },
      {
        name: "Maintenance",
        description:
          "Trim old revisions by retention window. Compact to max revision count. Dry-run before committing.",
      },
      {
        name: "Audit & Ops",
        description: "Review recent events and operational activity across the system.",
      },
      {
        name: "Dashboard",
        description:
          "System overview with health status, record counts, recent activity, and quick action links.",
      },
    ],
  },
];

const TIPS = [
  {
    title: "Demo mode",
    text: "Add ?demo=1 to the URL to browse with mock data (no backend required).",
  },
  {
    title: "Navigation",
    text: "Click breadcrumbs to go back. Use Escape for quick back navigation.",
  },
  {
    title: "JSON payloads",
    text: "Click any record row to expand and view its JSON payload inline.",
  },
  {
    title: "View presets",
    text: "Save frequently-used selectors in View Builder. Stored in browser localStorage.",
  },
  {
    title: "Dry run",
    text: "Always use dry-run mode first for Trim and Compact operations before committing.",
  },
  {
    title: "Token security",
    text: "Token values are shown only once at creation. Copy immediately.",
  },
];

export function HelpPage() {
  return (
    <ListPageLayout header={<PageHeader title="Help" />}>
      <div className="grid border-b border-border-strong lg:grid-cols-2">
        <section
          className="min-w-0 border-b border-border-strong px-4 py-5 lg:border-r lg:border-b-0 lg:px-6"
          aria-labelledby="keyboard-shortcuts-heading"
        >
          <SectionHeading id="keyboard-shortcuts-heading" icon={<Keyboard aria-hidden="true" />}>
            Keyboard shortcuts
          </SectionHeading>
          <div className="mt-3 overflow-hidden border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Key</TableHead>
                  <TableHead>Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {SHORTCUTS.map((shortcut) => (
                  <TableRow key={shortcut.key}>
                    <TableCell className="w-28">
                      <kbd className="inline-flex min-w-7 items-center justify-center rounded-sm border border-border-strong bg-panel-2 px-2 py-1 font-mono text-xs text-text">
                        {shortcut.key}
                      </kbd>
                    </TableCell>
                    <TableCell className="text-xs text-text-soft">{shortcut.description}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <p className="mt-3 text-xs leading-5 text-text-subtle">
            Shortcuts are disabled when a text input, modal, or dropdown is focused.
          </p>
        </section>

        <section className="px-4 py-5 lg:px-6" aria-labelledby="quick-tips-heading">
          <SectionHeading id="quick-tips-heading" icon={<Zap aria-hidden="true" />}>
            Quick tips
          </SectionHeading>
          <dl className="mt-3 divide-y divide-border border-y border-border">
            {TIPS.map((tip) => (
              <div key={tip.title} className="grid gap-1 py-3 sm:grid-cols-[8rem_1fr] sm:gap-4">
                <dt className="text-xs font-medium text-text-muted">{tip.title}</dt>
                <dd className="text-xs leading-5 text-text-subtle">{tip.text}</dd>
              </div>
            ))}
          </dl>
        </section>
      </div>

      <section
        className="border-b border-border-strong px-4 py-5 lg:px-6"
        aria-labelledby="screen-reference-heading"
      >
        <SectionHeading id="screen-reference-heading" icon={<Navigation aria-hidden="true" />}>
          Screen reference
        </SectionHeading>
        <div className="mt-4 grid divide-y divide-border border-y border-border lg:grid-cols-3 lg:divide-x lg:divide-y-0">
          {NAV_SECTIONS.map((section) => (
            <div key={section.title} className="px-0 py-4 lg:px-5 lg:first:pl-0 lg:last:pr-0">
              <h3 className="text-xs font-semibold text-text-muted">{section.title}</h3>
              <dl className="mt-3 space-y-4">
                {section.items.map((item) => (
                  <div key={item.name}>
                    <dt className="text-sm font-medium text-text">{item.name}</dt>
                    <dd className="mt-1 max-w-prose text-xs leading-5 text-text-subtle">
                      {item.description}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
          ))}
        </div>
      </section>

      <section className="px-4 py-5 lg:px-6" aria-labelledby="about-heading">
        <SectionHeading id="about-heading" icon={<Monitor aria-hidden="true" />}>
          About
        </SectionHeading>
        <dl className="mt-4 grid border-y border-border sm:grid-cols-2">
          <AboutItem label="Product">Tesseract — Content Memory Service</AboutItem>
          <AboutItem label="API base">
            <code className="font-mono">/v1</code>
          </AboutItem>
          <AboutItem label="Frontend">React 18 + TypeScript + Vite</AboutItem>
          <AboutItem label="Backend">Go + SQLite + go:embed</AboutItem>
        </dl>
      </section>
    </ListPageLayout>
  );
}

function SectionHeading({
  id,
  icon,
  children,
}: {
  id: string;
  icon: ReactNode;
  children: ReactNode;
}) {
  return (
    <h2 id={id} className="flex items-center gap-2 text-sm font-semibold text-text">
      <span className="text-text-muted [&>svg]:size-4">{icon}</span>
      {children}
    </h2>
  );
}

function AboutItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[6rem_1fr] gap-3 border-b border-border px-0 py-3 text-xs odd:sm:border-r odd:sm:pr-5 even:sm:pl-5 [&:nth-last-child(-n+2)]:sm:border-b-0">
      <dt className="text-text-subtle">{label}</dt>
      <dd className="text-text-soft">{children}</dd>
    </div>
  );
}
