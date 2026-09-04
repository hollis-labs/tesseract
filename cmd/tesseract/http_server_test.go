package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestDefaultHTTPServerTimeouts pins the production boundaries. Every existing
// contextapi test drives ServeHTTP through httptest.NewRecorder, so nothing
// else in the suite would notice if these went back to zero.
func TestDefaultHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler(), defaultHTTPServerTimeouts())

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout must be set — it is the Slowloris defence")
	}
	if srv.ReadHeaderTimeout > 30*time.Second {
		t.Errorf("ReadHeaderTimeout=%s is too generous to be a defence", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout must be set")
	}
	if srv.ReadTimeout < srv.ReadHeaderTimeout {
		t.Errorf("ReadTimeout=%s must not be shorter than ReadHeaderTimeout=%s", srv.ReadTimeout, srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout must be set")
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Error("MaxHeaderBytes must be explicit rather than inherited from net/http")
	}

	// WriteTimeout is deliberately unset. Several routes make synchronous LLM
	// or embedding calls on the request path, and an absolute write deadline
	// truncates those responses mid-flight. See newHTTPServer's comment before
	// changing this.
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout must stay unset (got %s) — read newHTTPServer's comment", srv.WriteTimeout)
	}
}

// TestHTTPServerClosesSlowHeaderWriter is the behavioural half: a client that
// opens a connection and never finishes its headers gets dropped rather than
// holding a goroutine forever. Driven through the same constructor with a
// short timeout so the test does not sit for the production ten seconds.
func TestHTTPServerClosesSlowHeaderWriter(t *testing.T) {
	timeouts := defaultHTTPServerTimeouts()
	timeouts.ReadHeader = 150 * time.Millisecond
	timeouts.Read = 300 * time.Millisecond

	ln, srv := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), timeouts)
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// A request line and one header, then nothing — the header block is never
	// terminated, which is exactly the Slowloris shape.
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nX-Dribble: a"); err != nil {
		t.Fatalf("write partial request: %v", err)
	}

	// The server must close the connection on its own. Give it well over the
	// configured timeout before declaring failure.
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err == nil && n > 0 {
		// A 408 Request Timeout followed by a close is also a pass — what
		// matters is that the connection did not stay open indefinitely.
		if _, err := conn.Read(buf); err == nil {
			t.Fatalf("server kept the half-open connection alive; read %q", buf[:n])
		}
		return
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("server did not close a connection that never finished its headers — ReadHeaderTimeout is not in effect")
	}
	// io.EOF or a reset: the server hung up, which is the behaviour under test.
}

// TestHTTPServerServesNormalRequestsOverRealListener is the counterweight: the
// boundaries must not interfere with an ordinary request/response, including
// one whose handler takes noticeably longer than a trivial route. A blanket
// WriteTimeout is what this would catch.
func TestHTTPServerServesNormalRequestsOverRealListener(t *testing.T) {
	timeouts := defaultHTTPServerTimeouts()
	timeouts.ReadHeader = 500 * time.Millisecond
	timeouts.Read = time.Second

	ln, srv := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Stand in for a synchronous LLM or embedding call: the handler holds
		// the response open well past any "fast" threshold.
		time.Sleep(1500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"echoed":%d}`, len(body))
	}), timeouts)
	defer func() { _ = srv.Close() }()

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Post("http://"+ln.Addr().String()+"/v1/synthesis/ask", "application/json",
		newReader(`{"question":"why"}`))
	if err != nil {
		t.Fatalf("slow handler request failed — a WriteTimeout would do exactly this: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(body) != `{"echoed":18}` {
		t.Fatalf("unexpected body %q", body)
	}
}

// TestHTTPServerGracefulShutdownDrainsInFlight covers the shutdown path
// runServe relies on: an in-flight request finishes, and the listener stops
// accepting new work.
func TestHTTPServerGracefulShutdownDrainsInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})

	ln, srv := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "drained")
		close(completed)
	}), defaultHTTPServerTimeouts())

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		res, err := client.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		resCh <- result{status: res.StatusCode, body: string(body)}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}

	shutdownErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- srv.Shutdown(ctx)
	}()

	// Shutdown must not kill the in-flight request.
	select {
	case <-completed:
		t.Fatal("handler completed before it was released")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	select {
	case err := <-shutdownErr:
		if err != nil {
			t.Fatalf("graceful shutdown returned %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("shutdown did not return")
	}

	select {
	case got := <-resCh:
		if got.err != nil {
			t.Fatalf("in-flight request was dropped by shutdown: %v", got.err)
		}
		if got.status != http.StatusOK || got.body != "drained" {
			t.Fatalf("in-flight request did not complete cleanly: status=%d body=%q", got.status, got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never returned")
	}

	// The listener is closed: new connections are refused.
	if conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := bufio.NewReader(conn).ReadString('\n'); err == nil {
			t.Error("server still answered after shutdown")
		}
		_ = conn.Close()
	}
}

// startTestServer binds a random loopback port and serves handler through the
// production constructor, so the tests exercise the same configuration
// runServe does.
func startTestServer(t *testing.T, handler http.Handler, timeouts httpServerTimeouts) (net.Listener, *http.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := newHTTPServer(ln.Addr().String(), handler, timeouts)
	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return ln, srv
}

func newReader(s string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, s)
		_ = pw.Close()
	}()
	return pr
}
