// Package jobs is the durable repository for transcoding jobs and live
// streams. Backed by the Store provider; ResumeAll walks non-terminal jobs
// at startup so the daemon picks up where it left off.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// BucketJobs is the BoltDB bucket holding job records (one JSON per ID).
const BucketJobs = "jobs"

// BucketStreams holds live-stream records.
const BucketStreams = "streams"

// Repo wraps a Store with job-specific accessors.
type Repo struct {
	Store store.Store
}

// New returns a Repo backed by s.
func New(s store.Store) *Repo { return &Repo{Store: s} }

// Save writes a Job. UpdatedAt is bumped to time.Now().UTC().
func (r *Repo) Save(ctx context.Context, j types.Job) error {
	if j.ID == "" {
		return errors.New("jobs: empty id")
	}
	j.UpdatedAt = time.Now().UTC()
	return store.PutJSON(ctx, r.Store, BucketJobs, j.ID, j)
}

// Get reads a Job by ID.
func (r *Repo) Get(ctx context.Context, id string) (types.Job, error) {
	var j types.Job
	if err := store.GetJSON(ctx, r.Store, BucketJobs, id, &j); err != nil {
		return j, err
	}
	return j, nil
}

// List returns every job (caller decides ordering / filtering).
func (r *Repo) List(ctx context.Context) ([]types.Job, error) {
	kvs, err := r.Store.List(ctx, BucketJobs)
	if err != nil {
		return nil, err
	}
	out := make([]types.Job, 0, len(kvs))
	for _, kv := range kvs {
		var j types.Job
		if err := jsonDecode(kv.Value, &j); err != nil {
			return nil, fmt.Errorf("decode %s: %w", kv.Key, err)
		}
		out = append(out, j)
	}
	return out, nil
}

// ListNonTerminal returns jobs whose Phase is not Complete / Error. Used by
// the runner's startup-resume.
func (r *Repo) ListNonTerminal(ctx context.Context) ([]types.Job, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Job, 0, len(all))
	for _, j := range all {
		if !j.Phase.IsTerminal() {
			out = append(out, j)
		}
	}
	return out, nil
}

// Transition writes a phase change with start time recorded.
func (r *Repo) Transition(ctx context.Context, id string, to types.JobPhase) (types.Job, error) {
	j, err := r.Get(ctx, id)
	if err != nil {
		return j, err
	}
	now := time.Now().UTC()
	// Close out the previous phase if any.
	if n := len(j.Phases); n > 0 && j.Phases[n-1].End.IsZero() {
		j.Phases[n-1].End = now
	}
	j.Phase = to
	j.Phases = append(j.Phases, types.PhaseTiming{Phase: to, Start: now})
	if to.IsTerminal() {
		// last phase ends with the same instant.
		j.Phases[len(j.Phases)-1].End = now
	}
	return j, r.Save(ctx, j)
}

// MarkError writes the terminal error phase + code/message.
func (r *Repo) MarkError(ctx context.Context, id, code, msg string) (types.Job, error) {
	j, err := r.Get(ctx, id)
	if err != nil {
		return j, err
	}
	j.ErrorCode = code
	j.ErrorMessage = msg
	now := time.Now().UTC()
	if n := len(j.Phases); n > 0 && j.Phases[n-1].End.IsZero() {
		j.Phases[n-1].End = now
	}
	j.Phase = types.PhaseError
	j.Phases = append(j.Phases, types.PhaseTiming{Phase: types.PhaseError, Start: now, End: now})
	if err := r.Save(ctx, j); err != nil {
		return j, err
	}
	return j, nil
}

// SaveStream writes a Stream.
func (r *Repo) SaveStream(ctx context.Context, s types.Stream) error {
	if s.WorkID == "" {
		return errors.New("jobs: empty work_id")
	}
	s.UpdatedAt = time.Now().UTC()
	return store.PutJSON(ctx, r.Store, BucketStreams, s.WorkID, s)
}

// GetStream reads a Stream by WorkID.
func (r *Repo) GetStream(ctx context.Context, workID string) (types.Stream, error) {
	var s types.Stream
	if err := store.GetJSON(ctx, r.Store, BucketStreams, workID, &s); err != nil {
		return s, err
	}
	return s, nil
}

// ListStreams returns every stream record.
func (r *Repo) ListStreams(ctx context.Context) ([]types.Stream, error) {
	kvs, err := r.Store.List(ctx, BucketStreams)
	if err != nil {
		return nil, err
	}
	out := make([]types.Stream, 0, len(kvs))
	for _, kv := range kvs {
		var s types.Stream
		if err := jsonDecode(kv.Value, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ListNonTerminalStreams returns streams that are still in flight (used
// by the live runner's restart-resume).
func (r *Repo) ListNonTerminalStreams(ctx context.Context) ([]types.Stream, error) {
	all, err := r.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Stream, 0, len(all))
	for _, s := range all {
		if !s.Phase.IsTerminal() {
			out = append(out, s)
		}
	}
	return out, nil
}

// IncrementDebitSeq atomically increments and persists the debitSeq for a
// stream. Returns the new value.
func (r *Repo) IncrementDebitSeq(ctx context.Context, workID string) (uint64, error) {
	s, err := r.GetStream(ctx, workID)
	if err != nil {
		return 0, err
	}
	s.DebitSeq++
	return s.DebitSeq, r.SaveStream(ctx, s)
}

// jsonDecode is a tiny wrapper that returns a clean error on decode.
func jsonDecode(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
