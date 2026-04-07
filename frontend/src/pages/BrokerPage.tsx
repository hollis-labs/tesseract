import { useState } from 'react';
import { Brain, Play, AlertTriangle } from 'lucide-react';
import { toast } from 'sonner';
import { brokerPlan } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import { JsonViewer } from '../components/ui/JsonViewer';
import type { BrokerPlanResponse } from '../api/types';

const INTENTS = [
  { value: 'resume_task', label: 'Resume Task', description: 'Gather context for resuming a specific task' },
  { value: 'boot_project', label: 'Boot Project', description: 'Load full project context for a new session' },
  { value: 'review_session', label: 'Review Session', description: 'Summarize a previous session' },
  { value: 'custom', label: 'Custom', description: 'Define a custom intent' },
];

interface Props {
  onExecutePlan?: (plan: BrokerPlanResponse) => void;
}

export function BrokerPage({ onExecutePlan }: Props) {
  const [intent, setIntent] = useState('resume_task');
  const [taskSummary, setTaskSummary] = useState('');
  const [nsConstraints, setNsConstraints] = useState('');
  const [maxItems, setMaxItems] = useState('50');
  const [maxTokens, setMaxTokens] = useState('8000');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [plan, setPlan] = useState<BrokerPlanResponse | null>(null);

  const handlePlan = async () => {
    setLoading(true);
    setError(null);
    setPlan(null);
    try {
      const res = await brokerPlan({
        intent,
        task_summary: taskSummary.trim() || undefined,
        namespace_constraints: nsConstraints.trim()
          ? nsConstraints.split(',').map(s => s.trim()).filter(Boolean)
          : undefined,
        budget: {
          max_items: parseInt(maxItems) || undefined,
          max_tokens_estimate: parseInt(maxTokens) || undefined,
        },
      });
      setPlan(res);
      toast.success('Plan generated');
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Plan failed: ${msg}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Broker</h2>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: plan ? '1fr 1fr' : '1fr', gap: '1rem' }}>
        {/* Form */}
        <div className="hud-panel" style={{ padding: '1rem' }}>
          <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.5rem' }}>Intent</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.3rem', marginBottom: '0.75rem' }}>
            {INTENTS.map(i => (
              <label
                key={i.value}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.5rem',
                  padding: '0.4rem 0.5rem',
                  cursor: 'pointer',
                  borderRadius: 'var(--radius-sm)',
                  background: intent === i.value ? 'rgba(var(--primary) / 0.08)' : 'transparent',
                  transition: 'background 0.1s',
                }}
              >
                <input
                  type="radio"
                  name="intent"
                  value={i.value}
                  checked={intent === i.value}
                  onChange={e => setIntent(e.target.value)}
                  style={{ accentColor: 'rgb(var(--primary))' }}
                />
                <div>
                  <div style={{ fontSize: '0.85rem' }}>{i.label}</div>
                  <div style={{ fontSize: '0.7rem', color: 'rgb(var(--muted))' }}>{i.description}</div>
                </div>
              </label>
            ))}
          </div>

          <div className="form-field">
            <label className="hud-label">Task Summary <span style={{ color: 'rgb(var(--muted))' }}>(optional)</span></label>
            <textarea
              className="hud-textarea"
              placeholder="Describe the task or context you need..."
              value={taskSummary}
              onChange={e => setTaskSummary(e.target.value)}
              style={{ width: '100%', minHeight: 60 }}
            />
          </div>

          <div className="form-grid" style={{ marginTop: '0.5rem' }}>
            <div className="form-field">
              <label className="hud-label">Namespace Constraints</label>
              <input className="hud-input" placeholder="user/memory/*, app/*" value={nsConstraints} onChange={e => setNsConstraints(e.target.value)} style={{ width: '100%' }} />
            </div>
            <div className="form-field">
              <label className="hud-label">Max Tokens</label>
              <input className="hud-input" type="number" value={maxTokens} onChange={e => setMaxTokens(e.target.value)} style={{ width: '100%' }} />
            </div>
          </div>

          <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.75rem' }}>
            <button className="hud-button-primary" onClick={handlePlan} disabled={loading}>
              <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
                {loading ? <Spinner size={13} /> : <Brain size={13} />} Generate Plan
              </span>
            </button>
          </div>
        </div>

        {/* Plan results */}
        {plan && (
          <div>
            {/* Rationale */}
            <div className="hud-panel" style={{ padding: '0.75rem', marginBottom: '0.75rem' }}>
              <div className="hud-label" style={{ marginBottom: '0.3rem' }}>Rationale</div>
              <div style={{ fontSize: '0.85rem', lineHeight: 1.5 }}>{plan.rationale}</div>
            </div>

            {/* Warnings */}
            {plan.warnings && plan.warnings.length > 0 && (
              <div className="hud-panel" style={{ padding: '0.75rem', marginBottom: '0.75rem', borderColor: 'rgba(var(--warn) / 0.4)' }}>
                <div className="hud-label" style={{ color: 'rgb(var(--warn))', marginBottom: '0.3rem', display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
                  <AlertTriangle size={12} /> Warnings
                </div>
                {plan.warnings.map((w, i) => (
                  <div key={i} style={{ fontSize: '0.8rem', color: 'rgb(var(--warn))', padding: '0.15rem 0' }}>
                    {w}
                  </div>
                ))}
              </div>
            )}

            {/* Generated selector */}
            <div className="hud-panel" style={{ padding: '0.75rem', marginBottom: '0.75rem' }}>
              <div className="hud-label" style={{ marginBottom: '0.3rem' }}>Generated Selector</div>
              <JsonViewer data={plan.selector} maxHeight="200px" />
            </div>

            {/* Assembly config */}
            <div className="hud-panel" style={{ padding: '0.75rem', marginBottom: '0.75rem' }}>
              <div className="hud-label" style={{ marginBottom: '0.3rem' }}>Assembly Config</div>
              <JsonViewer data={plan.assembly} maxHeight="200px" />
            </div>

            {/* Execute button */}
            {onExecutePlan && (
              <button className="hud-button-primary" onClick={() => onExecutePlan(plan)} style={{ width: '100%' }}>
                <span style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '0.3rem' }}>
                  <Play size={13} /> Execute Plan in Packet Builder
                </span>
              </button>
            )}
          </div>
        )}
      </div>

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginTop: '0.75rem' }}>
          {error}
        </div>
      )}
    </div>
  );
}
