import { Button, Callout, Input, JsonViewer, Label, Pill, Textarea } from "@hollis-labs/sysop-ui";
import { AlertTriangle, Brain, Play } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { brokerPlan } from "../api/client";
import type { BrokerPlanResponse } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

const INTENTS = [
  {
    value: "resume_task",
    label: "Resume task",
    description: "Gather context for resuming a specific task",
  },
  {
    value: "boot_project",
    label: "Boot project",
    description: "Load full project context for a new session",
  },
  { value: "review_session", label: "Review session", description: "Summarize a previous session" },
  { value: "custom", label: "Custom", description: "Define a custom intent" },
];

interface Props {
  onExecutePlan?: (plan: BrokerPlanResponse) => void;
}

export function BrokerPage({ onExecutePlan }: Props) {
  const [intent, setIntent] = useState("resume_task");
  const [taskSummary, setTaskSummary] = useState("");
  const [nsConstraints, setNsConstraints] = useState("");
  const [maxItems, setMaxItems] = useState("50");
  const [maxTokens, setMaxTokens] = useState("8000");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [plan, setPlan] = useState<BrokerPlanResponse | null>(null);

  const handlePlan = async () => {
    setLoading(true);
    setError(null);
    setPlan(null);
    try {
      const request: Parameters<typeof brokerPlan>[0] = { intent };
      const summary = taskSummary.trim();
      if (summary) request.task_summary = summary;
      const constraints = nsConstraints.trim();
      if (constraints) {
        request.namespace_constraints = constraints
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean);
      }
      const budget: NonNullable<Parameters<typeof brokerPlan>[0]["budget"]> = {};
      const itemBudget = parseInt(maxItems, 10);
      if (Number.isFinite(itemBudget) && itemBudget > 0) budget.max_items = itemBudget;
      const tokenBudget = parseInt(maxTokens, 10);
      if (Number.isFinite(tokenBudget) && tokenBudget > 0) {
        budget.max_tokens_estimate = tokenBudget;
      }
      if (Object.keys(budget).length > 0) request.budget = budget;
      const response = await brokerPlan(request);
      setPlan(response);
      toast.success("Plan generated");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      toast.error(`Plan failed: ${message}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-bg text-text">
      <div className="min-h-0 flex-1 overflow-auto">
        <section className="border-b border-border-strong px-4 py-4 lg:px-6">
          <h2 className="text-base font-semibold">Get a recommended retrieval plan</h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-text-soft">
            Describe the job and Broker will propose a selector and assembly setup for you to
            inspect before opening Packet Builder.
          </p>
        </section>

        <div className={plan ? "grid lg:grid-cols-2" : "grid"}>
          <section
            className={plan ? "min-w-0 border-b border-border-strong lg:border-r" : "min-w-0"}
          >
            <div className="grid border-b border-border md:grid-cols-2">
              <div className="px-4 py-3 md:border-r md:border-border lg:px-6">
                <p className="text-xs font-medium text-text-muted">When to use it</p>
                <p className="mt-1 text-xs leading-5 text-text-soft">
                  You know the intent, but want the system to propose the retrieval shape.
                </p>
              </div>
              <div className="border-t border-border px-4 py-3 md:border-t-0 lg:px-6">
                <p className="text-xs font-medium text-text-muted">Output</p>
                <p className="mt-1 text-xs leading-5 text-text-soft">
                  A selector and assembly config. Broker does not fetch the final packet.
                </p>
              </div>
            </div>

            <form
              className="space-y-5 bg-panel px-4 py-5 lg:px-6"
              onSubmit={(event) => {
                event.preventDefault();
                void handlePlan();
              }}
            >
              <fieldset>
                <legend className="text-xs font-semibold text-text-muted">Intent</legend>
                <div className="mt-3 divide-y divide-border rounded-md border border-border">
                  {INTENTS.map((option) => (
                    <label
                      key={option.value}
                      className="flex cursor-pointer items-start gap-3 bg-panel px-3 py-2.5 transition-colors first:rounded-t-md last:rounded-b-md hover:bg-panel-hover-soft has-checked:bg-panel-2"
                    >
                      <input
                        className="mt-1 size-3.5 accent-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        type="radio"
                        name="intent"
                        value={option.value}
                        checked={intent === option.value}
                        onChange={(event) => setIntent(event.target.value)}
                      />
                      <span className="min-w-0">
                        <span className="block text-sm text-text">{option.label}</span>
                        <span className="mt-0.5 block text-xs leading-5 text-text-subtle">
                          {option.description}
                        </span>
                      </span>
                    </label>
                  ))}
                </div>
              </fieldset>

              <div className="space-y-1.5">
                <Label htmlFor="broker-task-summary">
                  Task summary <span className="font-normal text-text-subtle">(optional)</span>
                </Label>
                <Textarea
                  id="broker-task-summary"
                  className="min-h-20"
                  placeholder="Describe the task or context you need..."
                  value={taskSummary}
                  onChange={(event) => setTaskSummary(event.target.value)}
                />
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-1.5 sm:col-span-2">
                  <Label htmlFor="broker-namespaces">Namespace constraints</Label>
                  <Input
                    id="broker-namespaces"
                    className="font-mono"
                    placeholder="user/memory/*, app/*"
                    value={nsConstraints}
                    onChange={(event) => setNsConstraints(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="broker-max-items">Max items</Label>
                  <Input
                    id="broker-max-items"
                    className="font-mono"
                    type="number"
                    min={1}
                    value={maxItems}
                    onChange={(event) => setMaxItems(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="broker-max-tokens">Max tokens</Label>
                  <Input
                    id="broker-max-tokens"
                    className="font-mono"
                    type="number"
                    min={1}
                    value={maxTokens}
                    onChange={(event) => setMaxTokens(event.target.value)}
                  />
                </div>
              </div>

              <div className="border-t border-border pt-4">
                <Button type="submit" disabled={loading}>
                  {loading ? <Spinner size={14} /> : <Brain aria-hidden="true" />}
                  Generate plan
                </Button>
                <p className="mt-3 max-w-3xl text-xs leading-5 text-text-subtle">
                  The result is advisory. Review the rationale and selector before assembling the
                  packet.
                </p>
              </div>
            </form>
          </section>

          {plan ? (
            <section className="min-w-0 bg-panel" aria-labelledby="broker-plan-heading">
              <div className="flex h-10 items-center justify-between border-b border-border px-4 lg:px-6">
                <h2 id="broker-plan-heading" className="text-xs font-semibold text-text-muted">
                  Recommended plan
                </h2>
                <Pill tone="success">Ready</Pill>
              </div>

              <div className="border-b border-border px-4 py-4 lg:px-6">
                <h3 className="text-xs font-semibold text-text-muted">Rationale</h3>
                <p className="mt-2 text-sm leading-6 text-text-soft">{plan.rationale}</p>
              </div>

              {plan.warnings && plan.warnings.length > 0 ? (
                <div className="border-b border-border px-4 py-4 lg:px-6">
                  <Callout
                    tone="warning"
                    title={`${plan.warnings.length} warning${plan.warnings.length === 1 ? "" : "s"}`}
                    icon={<AlertTriangle />}
                  >
                    <ul className="space-y-1">
                      {plan.warnings.map((warning) => (
                        <li key={warning}>{warning}</li>
                      ))}
                    </ul>
                  </Callout>
                </div>
              ) : null}

              <div className="border-b border-border px-4 py-4 lg:px-6">
                <h3 className="mb-2 text-xs font-semibold text-text-muted">Generated selector</h3>
                <JsonViewer value={plan.selector} className="max-h-52" />
              </div>

              <div className="border-b border-border px-4 py-4 lg:px-6">
                <h3 className="mb-2 text-xs font-semibold text-text-muted">Assembly config</h3>
                <JsonViewer value={plan.assembly} className="max-h-52" />
              </div>

              {onExecutePlan ? (
                <div className="px-4 py-4 lg:px-6">
                  <Button className="w-full" onClick={() => onExecutePlan(plan)}>
                    <Play aria-hidden="true" />
                    Execute plan in Packet Builder
                  </Button>
                </div>
              ) : null}
            </section>
          ) : null}
        </div>

        {error ? (
          <div className="border-t border-border-strong px-4 py-3 lg:px-6">
            <Callout tone="danger" title="Plan generation failed">
              {error}
            </Callout>
          </div>
        ) : null}
      </div>
    </div>
  );
}
