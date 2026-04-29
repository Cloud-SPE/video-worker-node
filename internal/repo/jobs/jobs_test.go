package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	return New(store.Memory())
}

func TestSaveAndGet(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, types.Job{ID: "j1", Phase: types.PhaseQueued, Mode: types.ModeVOD}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != types.PhaseQueued {
		t.Errorf("phase=%s", got.Phase)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must be set on save")
	}
	if err := r.Save(ctx, types.Job{}); err == nil {
		t.Fatal("expected empty-id error")
	}
	if _, err := r.Get(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestListAndNonTerminal(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	r.Save(ctx, types.Job{ID: "j1", Phase: types.PhaseQueued})
	r.Save(ctx, types.Job{ID: "j2", Phase: types.PhaseEncoding})
	r.Save(ctx, types.Job{ID: "j3", Phase: types.PhaseComplete})
	all, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("len=%d", len(all))
	}
	non, err := r.ListNonTerminal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(non) != 2 {
		t.Errorf("non-terminal=%d", len(non))
	}
}

func TestTransition(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	r.Save(ctx, types.Job{ID: "j", Phase: types.PhaseQueued})
	got, err := r.Transition(ctx, "j", types.PhaseDownloading)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != types.PhaseDownloading || len(got.Phases) != 1 {
		t.Fatalf("got=%+v", got)
	}
	got, _ = r.Transition(ctx, "j", types.PhaseEncoding)
	if len(got.Phases) != 2 {
		t.Errorf("phases=%d", len(got.Phases))
	}
	if got.Phases[0].End.IsZero() {
		t.Error("first phase should have End set on transition")
	}
	got, _ = r.Transition(ctx, "j", types.PhaseComplete)
	if !got.Phase.IsTerminal() {
		t.Error("not terminal")
	}
	if got.Phases[len(got.Phases)-1].End.IsZero() {
		t.Error("terminal phase should have End set immediately")
	}
}

func TestMarkError(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	r.Save(ctx, types.Job{ID: "j", Phase: types.PhaseEncoding})
	got, err := r.MarkError(ctx, "j", types.ErrCodeEncodingFailed, "boom")
	if err != nil {
		t.Fatal(err)
	}
	if got.ErrorCode != types.ErrCodeEncodingFailed || got.Phase != types.PhaseError {
		t.Fatalf("got=%+v", got)
	}
	if _, err := r.MarkError(ctx, "ghost", "X", "Y"); err == nil {
		t.Fatal("expected error on missing job")
	}
}

func TestStreamCRUD(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	if err := r.SaveStream(ctx, types.Stream{}); err == nil {
		t.Fatal("empty work_id should error")
	}
	r.SaveStream(ctx, types.Stream{WorkID: "s1", Phase: types.StreamPhaseStarting})
	r.SaveStream(ctx, types.Stream{WorkID: "s2", Phase: types.StreamPhaseClosed})
	r.SaveStream(ctx, types.Stream{WorkID: "s3", Phase: types.StreamPhaseStreaming})
	all, err := r.ListStreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("len=%d", len(all))
	}
	non, err := r.ListNonTerminalStreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(non) != 2 {
		t.Errorf("non-terminal=%d", len(non))
	}
	got, _ := r.GetStream(ctx, "s1")
	if got.Phase != types.StreamPhaseStarting {
		t.Errorf("phase=%s", got.Phase)
	}
}

func TestIncrementDebitSeq(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	r.SaveStream(ctx, types.Stream{WorkID: "s", DebitSeq: 0})
	for i := uint64(1); i <= 3; i++ {
		got, err := r.IncrementDebitSeq(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		if got != i {
			t.Errorf("got=%d want %d", got, i)
		}
	}
	if _, err := r.IncrementDebitSeq(ctx, "ghost"); err == nil {
		t.Fatal("expected error on missing stream")
	}
}

func TestSaveTimestampSet(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	before := time.Now().Add(-1 * time.Second)
	r.Save(ctx, types.Job{ID: "j"})
	got, _ := r.Get(ctx, "j")
	if got.UpdatedAt.Before(before) {
		t.Error("UpdatedAt should be recent")
	}
}
