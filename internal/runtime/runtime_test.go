package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubmitWebhookRunLimitsConcurrencyAndShowsPending(t *testing.T) {
	rt := New(1)
	ctx := context.Background()
	firstRelease := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	errs := make(chan error, 2)

	go func() {
		errs <- rt.SubmitWebhookRun(ctx, RunSpec{TicketRef: "issue-1", Branch: "branch-1"}, func(context.Context) error {
			close(firstStarted)
			<-firstRelease
			return nil
		})
	}()
	waitForClosed(t, firstStarted)

	go func() {
		errs <- rt.SubmitWebhookRun(ctx, RunSpec{TicketRef: "issue-2", Branch: "branch-2"}, func(context.Context) error {
			close(secondStarted)
			return nil
		})
	}()

	waitFor(t, func() bool {
		snap := rt.Snapshot()
		return snap.UsedSlots == 1 && len(snap.ActiveRuns) == 1 && len(snap.PendingRuns) == 1
	})
	select {
	case <-secondStarted:
		t.Fatal("second run started before the only slot was released")
	default:
	}

	close(firstRelease)
	waitForClosed(t, secondStarted)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("SubmitWebhookRun() error = %v", err)
		}
	}
}

func TestSubmitWebhookRunBlocksDuplicateTicketAndBranch(t *testing.T) {
	rt := New(2)
	firstRelease := make(chan struct{})
	firstStarted := make(chan struct{})
	errs := make(chan error, 3)

	go func() {
		errs <- rt.SubmitWebhookRun(context.Background(), RunSpec{TicketRef: "issue-1", Branch: "branch-1"}, func(context.Context) error {
			close(firstStarted)
			<-firstRelease
			return nil
		})
	}()
	waitForClosed(t, firstStarted)

	err := rt.SubmitWebhookRun(context.Background(), RunSpec{TicketRef: "issue-1", Branch: "branch-2"}, func(context.Context) error {
		t.Fatal("duplicate ticket run should not execute")
		return nil
	})
	if !errors.Is(err, ErrTicketActive) {
		t.Fatalf("duplicate ticket error = %v, want ErrTicketActive", err)
	}

	err = rt.SubmitWebhookRun(context.Background(), RunSpec{TicketRef: "issue-2", Branch: "branch-1"}, func(context.Context) error {
		t.Fatal("duplicate branch run should not execute")
		return nil
	})
	if !errors.Is(err, ErrBranchActive) {
		t.Fatalf("duplicate branch error = %v, want ErrBranchActive", err)
	}

	snap := rt.Snapshot()
	if len(snap.BlockedRuns) != 2 {
		t.Fatalf("blocked runs = %d, want 2", len(snap.BlockedRuns))
	}
	if snap.BlockedRuns[0].BlockReason != BlockReasonTicketActive || snap.BlockedRuns[1].BlockReason != BlockReasonBranchActive {
		t.Fatalf("blocked reasons = %q, %q", snap.BlockedRuns[0].BlockReason, snap.BlockedRuns[1].BlockReason)
	}

	close(firstRelease)
	if err := <-errs; err != nil {
		t.Fatalf("first run error = %v", err)
	}

	snap = rt.Snapshot()
	if len(snap.BlockedRuns) != 0 {
		t.Fatalf("blocked runs after conflict resolved = %d, want 0", len(snap.BlockedRuns))
	}
}

func TestSubmitWebhookRunRemovesTerminalRuns(t *testing.T) {
	rt := New(1)

	if err := rt.SubmitWebhookRun(context.Background(), RunSpec{TicketRef: "issue-1", Branch: "branch-1"}, func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("SubmitWebhookRun() error = %v", err)
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.runs) != 0 {
		t.Fatalf("retained runs = %d, want 0", len(rt.runs))
	}
}

func TestPauseResumeKeepsNewRunsPending(t *testing.T) {
	rt := New(1)
	rt.Pause()

	started := make(chan struct{})
	errs := make(chan error, 1)
	go func() {
		errs <- rt.SubmitWebhookRun(context.Background(), RunSpec{TicketRef: "issue-1", Branch: "branch-1"}, func(context.Context) error {
			close(started)
			return nil
		})
	}()

	waitFor(t, func() bool {
		snap := rt.Snapshot()
		return snap.Paused && snap.UsedSlots == 0 && len(snap.PendingRuns) == 1
	})
	select {
	case <-started:
		t.Fatal("run started while runtime was paused")
	default:
	}

	rt.Resume()
	waitForClosed(t, started)
	if err := <-errs; err != nil {
		t.Fatalf("SubmitWebhookRun() error = %v", err)
	}
}

func waitForClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel")
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
