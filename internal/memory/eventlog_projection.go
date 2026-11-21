package memory

import (
	"encoding/json"
	"time"
)

// EventLogFields are additive convenience aliases on event-list rows. They
// copy stored values only; the nested revision fields remain authoritative.
// Payload-derived aliases obey the requested projection's withholding rules.
type EventLogFields struct {
	ID            string    `json:"id"`
	At            time.Time `json:"at"`
	WorkstreamKey string    `json:"workstream_key,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	RunID         string    `json:"run_id,omitempty"`
	TeamID        string    `json:"team_id,omitempty"`
	State         string    `json:"state,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	Link          string    `json:"link,omitempty"`
}

type eventLogFullEntry struct {
	Revision
	EventLogFields
}
type eventLogProjectedEntry struct {
	ProjectedRevision
	EventLogFields
}

func eventLogFields(rev Revision, mode PayloadMode) EventLogFields {
	fields := EventLogFields{ID: rev.RevisionID, At: rev.CreatedAt}
	if mode == PayloadModeKeys {
		return fields
	}
	fields.Summary = rev.Payload.Summary
	var state map[string]json.RawMessage
	_ = json.Unmarshal(rev.ConsumerState, &state)
	read := func(key string) string { var value string; _ = json.Unmarshal(state[key], &value); return value }
	fields.WorkstreamKey = read("workstream_key")
	fields.TaskID = read("task_id")
	if _, exists := state["task_id"]; !exists {
		fields.TaskID = read("task")
	}
	fields.RunID = read("run_id")
	if _, exists := state["run_id"]; !exists {
		fields.RunID = read("run")
	}
	fields.TeamID = read("team_id")
	fields.State = read("state")
	if mode == PayloadModeFull {
		fields.Detail = rev.Payload.Body
		var data map[string]json.RawMessage
		_ = json.Unmarshal(rev.Payload.Data, &data)
		_ = json.Unmarshal(data["ref"], &fields.Link)
	}
	return fields
}

func projectFullEventLogEntries(revs []Revision) []eventLogFullEntry {
	out := make([]eventLogFullEntry, 0, len(revs))
	for _, rev := range revs {
		out = append(out, eventLogFullEntry{Revision: rev, EventLogFields: eventLogFields(rev, PayloadModeFull)})
	}
	return out
}

// MarshalJSON enriches the wire rows while preserving Entries' existing Go
// types for library callers of ProjectEventLogPage.
func (response EventLogResponse) MarshalJSON() ([]byte, error) {
	entries := response.Entries
	switch rows := entries.(type) {
	case []Revision:
		entries = projectFullEventLogEntries(rows)
	case []ProjectedRevision:
		out := make([]eventLogProjectedEntry, 0, len(rows))
		for _, row := range rows {
			rev := Revision{RevisionID: row.RevisionID, CreatedAt: row.CreatedAt, ConsumerState: row.ConsumerState}
			if row.Payload != nil {
				rev.Payload = *row.Payload
			}
			out = append(out, eventLogProjectedEntry{ProjectedRevision: row, EventLogFields: eventLogFields(rev, response.Manifest.PayloadMode)})
		}
		entries = out
	}
	return json.Marshal(struct {
		Entries  any                      `json:"entries"`
		Manifest EventLogResponseManifest `json:"manifest"`
	}{Entries: entries, Manifest: response.Manifest})
}
