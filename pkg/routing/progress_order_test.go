package routing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

type orderingProgress struct {
	calls   chan string
	release chan struct{}
}

func TestQueuedCardPrecedesRunningSnapshot(t *testing.T) {
	p := &orderedMessenger{calls: make(chan orderedCall, 4)}
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{}, nil, p, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	job := &activeJob{record: JobRecord{ID: "job-1", Class: JobClassWrite, State: JobStateQueued, Phase: "queued"}}
	queued := job.snapshot()
	job.mu.Lock()
	jm.queueProgress(job, queued)
	job.record.State, job.record.Phase = JobStateRunning, "thinking"
	job.mu.Unlock()
	first := <-p.calls
	defer releaseProgressCall(t, jm, first)
	if !strings.Contains(first.content, "force") {
		t.Fatalf("initial queued card was replaced by running: %s", first.content)
	}
}

func TestTerminalSupersedesUnsentInitialQueuedCard(t *testing.T) {
	p := &orderedMessenger{calls: make(chan orderedCall, 4)}
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{}, nil, p, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	job := &activeJob{record: JobRecord{ID: "job-1", Class: JobClassWrite, State: JobStateQueued, Phase: "queued"}}
	queued := job.snapshot()
	job.mu.Lock()
	jm.queueProgress(job, queued)
	job.record.State, job.record.Phase = JobStateDone, "done"
	job.mu.Unlock()
	first := <-p.calls
	defer releaseProgressCall(t, jm, first)
	if !strings.Contains(strings.ToLower(first.content), "done") {
		t.Fatalf("expected terminal card instead of queued: %s", first.content)
	}
}

func TestLatePreparingCreateRefreshesQueuedCard(t *testing.T) {
	p := &orderedMessenger{calls: make(chan orderedCall, 4)}
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{}, nil, p, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	target := LoopTarget{Workspace: t.TempDir()}.normalized()
	job := &activeJob{
		record: JobRecord{ID: "job-1", Class: JobClassWrite, State: JobStateQueued, Phase: "preparing_context", Detail: "Preparing attachments"},
		ctx:    context.Background(), cancel: func() {}, target: target,
	}
	jm.active[target.key()] = &targetActiveJobs{
		write:     &activeJob{record: JobRecord{ID: "writer", State: JobStateRunning}},
		preparing: []*activeJob{job},
	}
	jm.preparation <- struct{}{}
	jm.sendQueuedStatusAsync(context.Background(), job, JobRecord{}, 0)
	first := <-p.calls
	if !strings.Contains(strings.ToLower(first.content), "prepar") {
		close(first.release)
		t.Fatalf("expected preparing card: %s", first.content)
	}
	jm.prepareJob(job)
	if got := job.snapshot(); got.Phase != "queued" {
		close(first.release)
		t.Fatalf("preparation did not finish queued: %#v", got)
	}
	close(first.release)
	select {
	case next := <-p.calls:
		defer releaseProgressCall(t, jm, next)
		if next.id != "card" || !strings.Contains(strings.ToLower(next.content), "queued") {
			t.Fatalf("expected queued edit of created card, got id=%q content=%q", next.id, next.content)
		}
		stored, err := jm.store.All()
		if err != nil || len(stored) != 1 || stored[0].StatusMessageID != "card" {
			t.Fatalf("card ID not persisted before queued edit: %#v, %v", stored, err)
		}
	case <-time.After(time.Second):
		t.Fatal("late create left preparing card without queued edit")
	}
}

func releaseProgressCall(t *testing.T, jm *JobManager, call orderedCall) {
	t.Helper()
	close(call.release)
	deadline := time.Now().Add(time.Second)
	for len(jm.statusSlots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(jm.statusSlots) != 0 {
		t.Error("progress worker did not finish")
	}
}

type orderedCall struct {
	id       string
	content  string
	release  chan struct{}
	timedout <-chan struct{}
}

type orderedMessenger struct {
	calls chan orderedCall
}

func (p *orderedMessenger) SendOrEditProgress(ctx context.Context, _, _, id string, msg bus.OutboundMessage) (string, error) {
	call := orderedCall{id: id, content: msg.Content, release: make(chan struct{}), timedout: ctx.Done()}
	p.calls <- call
	<-call.release
	return "card", nil
}

func TestProgressTerminalSupersedesPendingAfterLateCall(t *testing.T) {
	for _, initialID := range []string{"", "card"} {
		for _, terminal := range []JobState{JobStateDone, JobStateCancelled, JobStateFailed} {
			t.Run("id="+initialID+"/"+string(terminal), func(t *testing.T) {
				p := &orderedMessenger{calls: make(chan orderedCall, 10)}
				jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{}, nil, p, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
				if err != nil {
					t.Fatal(err)
				}
				job := &activeJob{record: JobRecord{ID: "job-1", Class: JobClassWrite, State: JobStateQueued, Phase: "queued", StatusMessageID: initialID}}
				jm.sendQueuedStatusAsync(context.Background(), job, JobRecord{}, 0)
				first := <-p.calls
				if !strings.Contains(first.content, "force") || !strings.Contains(first.content, "cancel") {
					close(first.release)
					t.Fatal("queued card lost its controls")
				}
				reporter := jm.newProgressReporter(context.Background(), job)
				defer reporter.finish()
				done := make(chan struct{})
				go func() {
					reporter.update(JobStateRunning, "thinking", "Thinking")
					reporter.complete(terminal, string(terminal), string(terminal), nil)
					close(done)
				}()
				select {
				case <-done:
				case <-time.After(500 * time.Millisecond):
					close(first.release)
					t.Fatal("progress update blocked on messenger")
				}
				select {
				case <-first.timedout:
				case <-time.After(2 * time.Second):
					close(first.release)
					t.Fatal("messenger deadline did not expire")
				}
				if len(jm.progress.(*boundedProgress).slots) != 1 {
					close(first.release)
					t.Fatal("messenger slot released before the call returned")
				}
				select {
				case call := <-p.calls:
					close(first.release)
					close(call.release)
					t.Fatal("overlapping messenger calls")
				default:
				}
				close(first.release)
				select {
				case call := <-p.calls:
					defer close(call.release)
					if call.id != "card" || !strings.Contains(strings.ToLower(call.content), string(terminal)) {
						t.Fatalf("next call id=%q content=%q, want terminal edit of card", call.id, call.content)
					}
					stored, err := jm.store.All()
					if err != nil || len(stored) != 1 || stored[0].StatusMessageID != "card" {
						t.Fatalf("card ID not persisted before edit: %#v, %v", stored, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("terminal update not delivered")
				}
			})
		}
	}
}

func (p *orderingProgress) SendOrEditProgress(_ context.Context, _, _, id string, _ bus.OutboundMessage) (string, error) {
	p.calls <- id
	<-p.release
	return "card", nil
}

func TestProgressDoesNotOverlapOrBlockJob(t *testing.T) {
	p := &orderingProgress{calls: make(chan string, 20), release: make(chan struct{})}
	runner := &fakeJobRunner{started: make(chan struct{}), block: make(chan struct{})}
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{}, nil, p, func(LoopTarget) (JobRunner, error) { return runner, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	target := LoopTarget{Workspace: t.TempDir()}.normalized()
	defer func() {
		cancel()
		close(p.release)
		waitForNoActiveJobs(t, jm, target.key())
	}()
	if err := jm.Submit(ctx, target, bus.InboundMessage{Channel: "discord", ChatID: "room", Content: "work"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.calls:
	case <-time.After(time.Second):
		t.Fatal("initial progress did not start")
	}
	select {
	case <-runner.started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("job execution blocked on progress call")
	}
	select {
	case id := <-p.calls:
		t.Fatalf("overlapping progress call with id %q", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTerminalProgressCapacityFailureNotifies(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{MaxConcurrent: 1}, mb, &fakeProgressMessenger{}, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(jm.statusSlots); i++ {
		jm.statusSlots <- struct{}{}
	}
	job := &activeJob{record: JobRecord{ID: "job-1", ShortID: "00001", State: JobStateRunning, Channel: "discord", ChatID: "room"}}
	reporter := jm.newProgressReporter(context.Background(), job)
	defer reporter.finish()
	reporter.complete(JobStateDone, "done", "Done", nil)
	out := mustNextOutbound(t, mb)
	if !strings.Contains(out.Content, "Could not update its status card") || !strings.Contains(out.Content, "done") {
		t.Fatalf("missing failure notice: %#v", out)
	}
}

func TestTerminalAcceptedDuringIDPersistenceFailureIsReported(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	p := &orderedMessenger{calls: make(chan orderedCall, 1)}
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{}, mb, p, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	job := &activeJob{record: JobRecord{ID: "job-1", ShortID: "00001", State: JobStateQueued, Channel: "discord", ChatID: "room"}}
	jm.store.mu.Lock()
	jm.store.path = filepathJoin(t.TempDir(), "missing", "jobs.json")
	jm.sendQueuedStatusAsync(context.Background(), job, JobRecord{}, 0)
	first := <-p.calls
	close(first.release)
	deadline := time.Now().Add(time.Second)
	for job.snapshot().StatusMessageID == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if job.snapshot().StatusMessageID == "" {
		jm.store.mu.Unlock()
		t.Fatal("create did not return its ID")
	}
	job.update(func(r *JobRecord) { r.State, r.Phase = JobStateDone, "done" })
	if !jm.queueProgress(job) {
		jm.store.mu.Unlock()
		t.Fatal("terminal update was rejected")
	}
	jm.store.mu.Unlock()
	out := mustNextOutbound(t, mb)
	if !strings.Contains(strings.ToLower(out.Content), "done") {
		t.Fatalf("accepted terminal update lost without terminal notice: %#v", out)
	}
}

func TestTerminalAcceptedBeforeMessengerCapacityFailureIsReported(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	p := &orderedMessenger{calls: make(chan orderedCall, 1)}
	jm, err := NewJobManager(filepathJoin(t.TempDir(), "jobs.json"), config.JobsConfig{MaxConcurrent: 1}, mb, p, func(LoopTarget) (JobRunner, error) { return &fakeJobRunner{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	job := &activeJob{record: JobRecord{ID: "job-1", ShortID: "00001", State: JobStateQueued, Channel: "discord", ChatID: "room"}}
	jm.store.mu.Lock()
	jm.sendQueuedStatusAsync(context.Background(), job, JobRecord{}, 0)
	first := <-p.calls
	close(first.release)
	deadline := time.Now().Add(time.Second)
	for job.snapshot().StatusMessageID == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if job.snapshot().StatusMessageID == "" {
		jm.store.mu.Unlock()
		t.Fatal("create did not return its ID")
	}
	bounded := jm.progress.(*boundedProgress)
	for i := 0; i < cap(bounded.slots); i++ {
		bounded.slots <- struct{}{}
	}
	job.update(func(r *JobRecord) { r.State, r.Phase = JobStateDone, "done" })
	if !jm.queueProgress(job) {
		jm.store.mu.Unlock()
		t.Fatal("terminal update was rejected")
	}
	jm.store.mu.Unlock()
	out := mustNextOutbound(t, mb)
	if !strings.Contains(strings.ToLower(out.Content), "done") {
		t.Fatalf("accepted terminal update lost without terminal notice: %#v", out)
	}
}
