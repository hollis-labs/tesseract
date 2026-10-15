package authn

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
)

// maxSummaryKeys bounds memory: a scanner cannot grow the table without limit.
const maxSummaryKeys = 512

// DefaultSummaryInterval is how often a non-enforcing process logs what it
// would have denied since the last line.
const DefaultSummaryInterval = 5 * time.Minute

// Caller is one distinct would-deny shape. It holds route/tool names, the
// authentication state, the required scope and a sanitized User-Agent. It
// never holds a bearer, a query string or a request body.
type Caller struct {
	Surface        string `json:"surface"`          // "http" | "mcp"
	Method         string `json:"method,omitempty"` // http
	Route          string `json:"route,omitempty"`  // route-table pattern, or tool name
	Authentication string `json:"authentication"`   // missing | invalid | unavailable | off-mode | verified
	Principal      string `json:"principal,omitempty"`
	RequiredScope  string `json:"required_scope,omitempty"`
	Reason         string `json:"reason,omitempty"`
	UserAgent      string `json:"user_agent,omitempty"`
	Count          int    `json:"count"`
	First          string `json:"first_seen"`
	Last           string `json:"last_seen"`
}

// Summary aggregates would-deny events for permissive and off modes. It is the
// answer to "which callers would break if I flipped to enforce".
type Summary struct {
	mu      sync.Mutex
	rows    map[string]*Caller
	dropped int
	logged  map[string]int // counts at last log line, to print only what changed
	now     func() time.Time
}

// NewSummary returns an empty summary.
func NewSummary() *Summary {
	return &Summary{rows: map[string]*Caller{}, logged: map[string]int{}, now: time.Now}
}

// Audit is the go-auth Config.Audit callback (MCP tools, and HTTP when no
// request detail is available). HTTP requests are recorded through
// RecordRequest instead, which adds method, route pattern and user agent.
func (s *Summary) Audit() func(auth.Event) {
	return func(ev auth.Event) {
		if ev.Tool == "" {
			return // HTTP events are recorded via RecordRequest
		}
		s.record(Caller{Surface: "mcp", Route: ev.Tool}, ev)
	}
}

// RecordRequest records one guarded HTTP request. route is the route-table
// pattern (contextapi.RoutePattern), not the raw path, so item ids do not
// multiply rows.
func (s *Summary) RecordRequest(r *http.Request, route string, ev auth.Event) {
	if route == "/v1/auth/summary" || route == "/v1/auth/whoami" {
		return // reading the report must not add to it
	}
	s.record(Caller{
		Surface: "http", Method: r.Method, Route: route,
		UserAgent: sanitizeUA(r.UserAgent()),
	}, ev)
}

func (s *Summary) record(c Caller, ev auth.Event) {
	// Interesting = the caller would be rejected under enforce, or it was
	// attributed to the synthetic off-mode operator.
	if !ev.WouldDeny && ev.Authentication != auth.StateOffMode {
		return
	}
	c.Authentication, c.Principal, c.RequiredScope, c.Reason = ev.Authentication, ev.PrincipalID, ev.RequiredScope, ev.Reason
	key := strings.Join([]string{c.Surface, c.Method, c.Route, c.Authentication, c.Principal, c.RequiredScope, c.UserAgent}, "|")
	now := s.now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.rows[key]; ok {
		row.Count++
		row.Last = now
		return
	}
	if len(s.rows) >= maxSummaryKeys {
		s.dropped++
		return
	}
	c.Count, c.First, c.Last = 1, now, now
	s.rows[key] = &c
}

// Snapshot returns the rows, busiest first, and the number of events dropped
// for lack of room.
func (s *Summary) Snapshot() (rows []Caller, dropped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows = make([]Caller, 0, len(s.rows))
	for _, r := range s.rows {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Route < rows[j].Route
	})
	return rows, s.dropped
}

// Run logs a summary line block every interval while rows changed, and a final
// block on shutdown. Output is metadata only.
func (s *Summary) Run(ctx context.Context, w io.Writer, interval time.Duration) {
	if w == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultSummaryInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flush(w, true)
			return
		case <-t.C:
			s.flush(w, false)
		}
	}
}

func (s *Summary) flush(w io.Writer, final bool) {
	rows, dropped := s.Snapshot()
	s.mu.Lock()
	changed := false
	for _, r := range rows {
		k := r.Surface + "|" + r.Method + "|" + r.Route + "|" + r.Authentication + "|" + r.Principal + "|" + r.RequiredScope + "|" + r.UserAgent
		if s.logged[k] != r.Count {
			changed = true
		}
		s.logged[k] = r.Count
	}
	s.mu.Unlock()
	if len(rows) == 0 || (!changed && !final) {
		return
	}
	_, _ = fmt.Fprintf(w, "auth: would-deny summary (%d distinct callers%s):\n", len(rows), map[bool]string{true: ", final", false: ""}[final])
	for i, r := range rows {
		if i == 20 {
			_, _ = fmt.Fprintf(w, "auth:   ... %d more (GET /v1/auth/summary)\n", len(rows)-i)
			break
		}
		_, _ = fmt.Fprintf(w, "auth:   %s\n", r.String())
	}
	if dropped > 0 {
		_, _ = fmt.Fprintf(w, "auth:   (%d events not recorded: table full)\n", dropped)
	}
}

// String renders one row on one line.
func (c Caller) String() string {
	who := c.Principal
	if who == "" {
		who = "(no bearer)"
	}
	target := c.Route
	if c.Method != "" {
		target = c.Method + " " + c.Route
	}
	s := fmt.Sprintf("%dx %s %s auth=%s who=%s", c.Count, c.Surface, target, c.Authentication, who)
	if c.RequiredScope != "" {
		s += " needs=" + c.RequiredScope
	}
	if c.UserAgent != "" {
		s += " ua=" + c.UserAgent
	}
	return s
}

// sanitizeUA keeps the first 80 printable ASCII characters of a User-Agent so
// a hostile client cannot inject control characters or flood the log.
func sanitizeUA(ua string) string {
	var b strings.Builder
	for _, r := range ua {
		if b.Len() >= 80 {
			break
		}
		if r >= 0x20 && r < 0x7f && r != '|' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
