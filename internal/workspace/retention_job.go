package workspace

import (
	"context"
	"time"
)

// RetentionJob runs only when the daemon explicitly wires it from validated
// operator configuration. Store construction never starts this job.
type RetentionJob struct {
	Store     *Store
	Settings  RetentionSettings
	Interval  time.Duration
	BatchSize int
	Logger    func(string, ...any)
}

func (j *RetentionJob) Run(ctx context.Context) {
	if j == nil || j.Store == nil || j.Interval <= 0 {
		return
	}
	ticker := time.NewTicker(j.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := j.Store.RunRetentionPass(ctx, j.Settings, j.BatchSize)
			if err != nil {
				if j.Logger != nil {
					j.Logger("workspace retention pass failed: %v", err)
				}
				continue
			}
			if j.Logger != nil {
				j.Logger("workspace retention pass complete: purged=%d skipped=%d", report.Purged, report.Skipped)
			}
		}
	}
}
