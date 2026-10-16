package contextstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/security/auth"
)

// auditSubject maps an audit event to the envelope subject URN and the
// payload subject{type,id}. The URN is msg://<type>/tesseract/<id>.
//
// TODO(CW-20261016-0140 step 2): switch memory|knowledge|workspace|event
// subjects to the stable item_id BEFORE any drainer or SSE ships (architect,
// tesseract_outbox_step1_subject_and_coupling_2026_10_16). The namespace/key
// form is accepted as interim only.
//
// The ruling asks for the stable item id for memory|knowledge|workspace|event
// subjects. The audit choke point does not carry it (it receives namespace,
// key and the revision id), so those subjects use namespace/key for now and the
// revision id travels in payload ref. Threading the item id comes with the
// per-surface mutation-transaction work in step 2.
func auditSubject(ev AuditEvent) (typ, id string) {
	action := ev.EventType
	switch {
	case strings.HasPrefix(action, "namespace."):
		return "namespace", escapeSegments(ev.Namespace)
	case strings.HasPrefix(action, "credential."):
		return "credential", url.PathEscape(ev.Key)
	}
	for _, d := range []string{"memory", "knowledge", "workspace", "event"} {
		if strings.HasPrefix(action, d+".") {
			return d, subjectID(ev.Namespace, ev.Key)
		}
	}
	return "context", subjectID(ev.Namespace, ev.Key)
}

// subjectID joins namespace and key, tolerating an empty namespace (a packet
// request with no selector namespace; CW-20261016-0149 makes that valid).
func subjectID(ns, key string) string {
	if ns == "" {
		return url.PathEscape(key)
	}
	return escapeSegments(ns) + "/" + url.PathEscape(key)
}

// escapeSegments PathEscapes each "/"-separated segment, keeping the
// separators, so a namespace with spaces or reserved characters cannot forge or
// split a subject URN. This is the interim form until item_id replaces
// namespace/key (TODO above).
func escapeSegments(ns string) string {
	parts := strings.Split(ns, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// auditActor derives the envelope actor. The authenticated principal in ctx is
// the only source of a verified identity; a request-body actor string never
// fills actor (it is kept as payload claimed_actor by the caller).
func auditActor(ctx context.Context, claimed string) (*runtimeevents.Actor, string) {
	if p, ok := auth.FromContext(ctx); ok && p.Verified && strings.TrimSpace(p.ID) != "" {
		// The kind comes from the URN authority (msg://<authority>/...). An
		// authority outside agent|user|service|tool (a device, say) cannot be
		// expressed in the mesh vocabulary, and a guessed kind makes Validate
		// reject the envelope, so the actor is omitted rather than invented.
		urn := p.ID
		if !strings.HasPrefix(urn, "msg://") {
			if !validActorKind(p.Kind) {
				return nil, claimedActorFor(claimed, p.ID)
			}
			urn = "msg://" + p.Kind + "/tesseract/" + urn
		}
		kind := urnAuthority(urn)
		if !validActorKind(kind) {
			return nil, claimedActorFor(claimed, p.ID)
		}
		return &runtimeevents.Actor{URN: urn, Kind: kind, Verified: "token"}, claimedActorFor(claimed, p.ID)
	}
	switch {
	case claimed == "cli":
		return &runtimeevents.Actor{URN: "msg://user/local/operator", Kind: "user", Role: "operator", Verified: "claimed"}, ""
	case claimed == "system" || strings.HasPrefix(claimed, "credential:"):
		// Internal paths only: nothing a caller sent.
		return &runtimeevents.Actor{URN: "msg://service/tesseract/system", Kind: "service", Role: "system", Verified: "none"}, ""
	}
	// An unauthenticated call has no attributable actor; what it claimed stays
	// in the payload as claimed_actor.
	return nil, claimedActorFor(claimed, "")
}

func urnAuthority(urn string) string {
	rest := strings.TrimPrefix(urn, "msg://")
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

func claimedActorFor(claimed, verifiedID string) string {
	if claimed == "" || claimed == verifiedID || claimed == "system" || claimed == "cli" || strings.HasPrefix(claimed, "credential:") {
		return ""
	}
	return claimed
}

func validActorKind(k string) bool {
	switch k {
	case "agent", "user", "service", "tool":
		return true
	}
	return false
}

// outboxMapFault is a test seam, nil in production. A valid audit event
// cannot currently fail envelope mapping, so the best-effort path (audit row
// kept, envelope skipped) is only reachable by injecting the failure.
var outboxMapFault func() error

// outboxRecordHook and outboxEnvelopeHook are test seams, nil in production.
// They let a test corrupt the record before mapping (missing subject) or the
// envelope after it (a v1.1 contract violation) to reach the failure paths a
// valid audit event cannot.
var (
	outboxRecordHook   func(*OutboxRecord)
	outboxEnvelopeHook func(*runtimeevents.Event)
)

// OutboxRecord describes one audit.recorded fact for the outbox. It is the
// shape both the audit choke point and later per-surface writers (touch, step
// 2) hand to AppendOutbox, so envelope construction lives in one place.
//
// Payload carries ids only, never record content. CorrelationID, when set, is
// shared by every record that belongs to one call (one touch call fans out to
// one event per workstream, all with the same correlation id).
type OutboxRecord struct {
	Action        string // producer action name, e.g. "write" or "touch"
	Outcome       string // ok (default), denied, failed
	SubjectType   string
	SubjectID     string
	Ref           string
	Namespace     string
	WorkstreamID  string // envelope scope.workstream_id
	CorrelationID string // envelope corr.correlation_id
	Actor         string // claimed actor string; the verified principal in ctx wins
	OccurredAt    string // RFC3339; empty means now
	AuditEventID  int64  // link to audit_events.id when there is one
	Extra         map[string]any
}

// recordFromAudit converts an audit event to an outbox record.
func recordFromAudit(ev AuditEvent) OutboxRecord {
	typ, id := auditSubject(ev)
	ref := ev.RecordID
	if ref == "" && ev.Revision > 0 {
		ref = "rev:" + strconv.FormatInt(ev.Revision, 10)
	}
	return OutboxRecord{Action: ev.EventType, SubjectType: typ, SubjectID: id, Ref: ref,
		Namespace: ev.Namespace, Actor: ev.Actor, OccurredAt: ev.CreatedAt}
}

// buildEnvelope renders one record as an audit.recorded envelope. Sequence is
// set by the caller once the outbox row exists.
func buildEnvelope(ctx context.Context, r OutboxRecord, evtID string, generation uint64) (runtimeevents.Event, string, error) {
	if outboxMapFault != nil {
		if err := outboxMapFault(); err != nil {
			return runtimeevents.Event{}, "", err
		}
	}
	if outboxRecordHook != nil {
		outboxRecordHook(&r)
	}
	if r.Action == "" || r.SubjectType == "" || r.SubjectID == "" {
		return runtimeevents.Event{}, "", errors.New("outbox: action and subject are required")
	}
	// A subject must name one thing. A keyless item would otherwise become a
	// bare-namespace subject ("ns/"), which looks like a namespace event; it is
	// a mapping error instead (skipped and recorded for repair by the caller).
	if strings.HasSuffix(r.SubjectID, "/") || strings.HasPrefix(r.SubjectID, "/") || strings.Contains(r.SubjectID, "//") {
		return runtimeevents.Event{}, "", errors.New("outbox: subject id has an empty segment: " + r.SubjectID)
	}
	subject := "msg://" + r.SubjectType + "/tesseract/" + r.SubjectID
	actor, claimed := auditActor(ctx, r.Actor)
	outcome := r.Outcome
	if outcome == "" {
		outcome = "ok"
	}
	payload := map[string]any{}
	for k, v := range r.Extra {
		payload[k] = v
	}
	payload["action"] = r.Action
	payload["outcome"] = outcome
	payload["subject"] = map[string]string{"type": r.SubjectType, "id": r.SubjectID}
	if r.Ref != "" {
		payload["ref"] = r.Ref
	}
	if claimed != "" {
		payload["claimed_actor"] = claimed
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return runtimeevents.Event{}, "", err
	}

	t := time.Now().UTC()
	if r.OccurredAt != "" {
		if parsed, perr := time.Parse(time.RFC3339, r.OccurredAt); perr == nil {
			t = parsed.UTC()
		}
	}
	env := runtimeevents.Event{
		SchemaVersion: "1",
		ID:            evtID,
		Kind:          runtimeevents.EventKind(outboxKindAudit),
		Time:          t,
		App:           outboxApp,
		Subject:       subject,
		Generation:    generation,
		Source:        runtimeevents.Source{Channel: outboxChannelAudit},
		Payload:       raw,
		Actor:         actor,
		Class:         &runtimeevents.Class{Category: "audit", PayloadSchema: "urn:hollis-labs:events:audit.recorded@1"},
	}
	if r.WorkstreamID != "" {
		env.Scope = &runtimeevents.Scope{WorkstreamID: r.WorkstreamID}
	}
	if r.CorrelationID != "" {
		env.Corr = &runtimeevents.Corr{CorrelationID: r.CorrelationID}
	}
	if outboxEnvelopeHook != nil {
		outboxEnvelopeHook(&env)
	}
	return env, subject, nil
}
