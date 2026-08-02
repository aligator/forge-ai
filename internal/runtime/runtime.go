package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusBlocked   Status = "blocked"
)

type BlockReason string

const (
	BlockReasonTicketActive BlockReason = "ticket_active"
	BlockReasonBranchActive BlockReason = "branch_active"
)

var (
	ErrTicketActive = errors.New("ticket already active")
	ErrBranchActive = errors.New("branch already active")
	ErrRunCanceled  = errors.New("run canceled")
)

type RunSpec struct {
	TicketRef string
	Branch    string
	Owner     string
	Repo      string
	Kind      string
	Number    int
}

type Run struct {
	ID          string
	TicketRef   string
	Branch      string
	Owner       string
	Repo        string
	Kind        string
	Number      int
	Status      Status
	BlockReason BlockReason
	Error       string
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
}

type Snapshot struct {
	Paused         bool
	MaxConcurrent  int
	UsedSlots      int
	ActiveRuns     []Run
	PendingRuns    []Run
	BlockedRuns    []Run
	ActiveTickets  []string
	ActiveBranches []string
}

type Runtime struct {
	mu             sync.Mutex
	cond           *sync.Cond
	maxConcurrent  int
	paused         bool
	nextID         int64
	usedSlots      int
	runs           map[string]*runState
	activeTickets  map[string]string
	activeBranches map[string]string
}

type runState struct {
	Run
	cancel context.CancelFunc
}

func New(maxConcurrent int) *Runtime {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	r := &Runtime{
		maxConcurrent:  maxConcurrent,
		runs:           make(map[string]*runState),
		activeTickets:  make(map[string]string),
		activeBranches: make(map[string]string),
	}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *Runtime) SubmitWebhookRun(ctx context.Context, spec RunSpec, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("runtime run function is nil")
	}

	runCtx, cancel := context.WithCancel(ctx)
	state := r.accept(spec, cancel)

	defer cancel()

	if err := r.waitForSlot(runCtx, state.ID); err != nil {
		r.finish(state.ID, StatusCanceled, err)
		return err
	}

	err := fn(runCtx)
	if err != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			r.finish(state.ID, StatusCanceled, err)
		} else {
			r.finish(state.ID, StatusFailed, err)
		}
		return err
	}
	r.finish(state.ID, StatusSucceeded, nil)
	return nil
}

func (r *Runtime) SetMaxConcurrent(maxConcurrent int) error {
	if maxConcurrent <= 0 {
		return fmt.Errorf("max concurrent must be positive, got %d", maxConcurrent)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxConcurrent = maxConcurrent
	r.cond.Broadcast()
	return nil
}

func (r *Runtime) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = true
}

func (r *Runtime) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = false
	r.cond.Broadcast()
}

func (r *Runtime) CancelRun(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.runs[id]
	if state == nil {
		return false
	}
	switch state.Status {
	case StatusPending, StatusRunning, StatusBlocked:
	default:
		return false
	}
	if state.cancel != nil {
		state.cancel()
	}
	return true
}

func (r *Runtime) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := Snapshot{
		Paused:        r.paused,
		MaxConcurrent: r.maxConcurrent,
		UsedSlots:     r.usedSlots,
	}
	for _, state := range r.runs {
		run := cloneRun(state)
		switch run.Status {
		case StatusPending:
			snap.PendingRuns = append(snap.PendingRuns, run)
		case StatusRunning:
			snap.ActiveRuns = append(snap.ActiveRuns, run)
		case StatusBlocked:
			snap.BlockedRuns = append(snap.BlockedRuns, run)
		}
	}
	for ticket := range r.activeTickets {
		snap.ActiveTickets = append(snap.ActiveTickets, ticket)
	}
	for branch := range r.activeBranches {
		snap.ActiveBranches = append(snap.ActiveBranches, branch)
	}
	sortRuns(snap.PendingRuns)
	sortRuns(snap.ActiveRuns)
	sortRuns(snap.BlockedRuns)
	sort.Strings(snap.ActiveTickets)
	sort.Strings(snap.ActiveBranches)
	return snap
}

func (r *Runtime) accept(spec RunSpec, cancel context.CancelFunc) *runState {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	state := &runState{Run: Run{
		ID:        fmt.Sprintf("run-%d", r.nextID),
		TicketRef: spec.TicketRef,
		Branch:    spec.Branch,
		Owner:     spec.Owner,
		Repo:      spec.Repo,
		Kind:      spec.Kind,
		Number:    spec.Number,
		Status:    StatusPending,
		CreatedAt: time.Now(),
	}}
	state.cancel = cancel
	r.runs[state.ID] = state

	if spec.TicketRef != "" {
		if _, exists := r.activeTickets[spec.TicketRef]; exists {
			state.Status = StatusBlocked
			state.BlockReason = BlockReasonTicketActive
			return state
		}
	}
	if spec.Branch != "" {
		if _, exists := r.activeBranches[spec.Branch]; exists {
			state.Status = StatusBlocked
			state.BlockReason = BlockReasonBranchActive
			return state
		}
	}
	if spec.TicketRef != "" {
		r.activeTickets[spec.TicketRef] = state.ID
	}
	if spec.Branch != "" {
		r.activeBranches[spec.Branch] = state.ID
	}
	return state
}

func (r *Runtime) waitForSlot(ctx context.Context, id string) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)

	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		state := r.runs[id]
		if state == nil {
			return ErrRunCanceled
		}
		if err := ctx.Err(); err != nil {
			r.releaseLocked(state)
			return err
		}
		if state.Status == StatusBlocked {
			if state.TicketRef != "" {
				if _, exists := r.activeTickets[state.TicketRef]; exists {
					state.BlockReason = BlockReasonTicketActive
					r.cond.Wait()
					continue
				}
			}
			if state.Branch != "" {
				if _, exists := r.activeBranches[state.Branch]; exists {
					state.BlockReason = BlockReasonBranchActive
					r.cond.Wait()
					continue
				}
			}
			if state.TicketRef != "" {
				r.activeTickets[state.TicketRef] = state.ID
			}
			if state.Branch != "" {
				r.activeBranches[state.Branch] = state.ID
			}
			state.Status = StatusPending
			state.BlockReason = ""
		}
		if !r.paused && r.usedSlots < r.maxConcurrent {
			r.usedSlots++
			state.Status = StatusRunning
			state.StartedAt = time.Now()
			return nil
		}
		r.cond.Wait()
	}
}

func (r *Runtime) finish(id string, status Status, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.runs[id]
	if state == nil {
		return
	}
	if state.Status == StatusRunning && r.usedSlots > 0 {
		r.usedSlots--
	}
	if err != nil {
		state.Error = err.Error()
	}
	state.Status = status
	state.FinishedAt = time.Now()
	r.releaseLocked(state)
	delete(r.runs, id)
	r.cond.Broadcast()
}

func (r *Runtime) releaseLocked(state *runState) {
	if state.TicketRef != "" && r.activeTickets[state.TicketRef] == state.ID {
		delete(r.activeTickets, state.TicketRef)
	}
	if state.Branch != "" && r.activeBranches[state.Branch] == state.ID {
		delete(r.activeBranches, state.Branch)
	}
}

func cloneRun(state *runState) Run {
	run := state.Run
	return run
}

func sortRuns(runs []Run) {
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}
