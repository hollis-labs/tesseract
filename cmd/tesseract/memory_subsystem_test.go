package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMemorySubsystemCloseWaitsForQueueAndDecayWorkerTermination(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open queue DB: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping queue DB: %v", err)
	}

	lifecycleCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subsystem := &memorySubsystem{
		queueDB:      db,
		lifecycleCtx: lifecycleCtx,
		cancel:       cancel,
	}

	cancellationSeen := make(chan string, 2)
	releaseWorkers := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseWorkers) })
		_ = subsystem.Close()
	})
	for _, name := range []string{"queue", "decay"} {
		workerName := name
		subsystem.startWorker(func(ctx context.Context) {
			<-ctx.Done()
			cancellationSeen <- workerName
			<-releaseWorkers
		})
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- subsystem.Close()
	}()

	seen := make(map[string]bool, 2)
	for range 2 {
		select {
		case name := <-cancellationSeen:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("Close did not cancel both background workers")
		}
	}
	if !seen["queue"] || !seen["decay"] {
		t.Fatalf("Close cancellation reached workers %v; want queue and decay", seen)
	}

	// Both workers have observed cancellation but are deliberately still
	// running. The shutdown barrier must keep their queue DB open until they
	// return, and Close must still be blocked.
	if err := db.Ping(); err != nil {
		t.Fatalf("queue DB closed before workers terminated: %v", err)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before queue and decay workers terminated: %v", err)
	default:
	}

	releaseOnce.Do(func() { close(releaseWorkers) })
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return after queue and decay workers terminated")
	}
	if err := db.Ping(); err == nil {
		t.Fatal("queue DB remains open after worker termination")
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
