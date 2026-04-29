// Package store provides a minimal key-value Store interface plus a
// BoltDB-backed implementation (production) and a memory implementation
// (tests / dev mode).
//
// The Store is the only durable-state surface in the daemon. Per core
// belief #8, every transcoding job, every live-stream session, every
// debitSeq counter must round-trip the Store before it counts.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	bbolt "go.etcd.io/bbolt"
)

// ErrNotFound is returned when Get does not find the key.
var ErrNotFound = errors.New("store: key not found")

// Store is the daemon-internal durable-state surface.
type Store interface {
	// Put writes value under (bucket, key). Creates bucket if absent.
	Put(ctx context.Context, bucket, key string, value []byte) error
	// Get reads (bucket, key); returns ErrNotFound if absent.
	Get(ctx context.Context, bucket, key string) ([]byte, error)
	// Delete removes (bucket, key). No error if absent.
	Delete(ctx context.Context, bucket, key string) error
	// List returns every key→value pair in the bucket (sorted by key).
	List(ctx context.Context, bucket string) ([]KV, error)
	// Close releases the underlying handle.
	Close() error
}

// KV is one key-value pair from List().
type KV struct {
	Key   string
	Value []byte
}

// PutJSON marshals v as JSON and writes it under (bucket, key).
func PutJSON(ctx context.Context, s Store, bucket, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return s.Put(ctx, bucket, key, b)
}

// GetJSON reads (bucket, key) and unmarshals into v.
func GetJSON(ctx context.Context, s Store, bucket, key string, v any) error {
	b, err := s.Get(ctx, bucket, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Memory returns a thread-safe in-memory Store. Used by tests and dev mode.
func Memory() Store { return &memStore{m: map[string]map[string][]byte{}} }

type memStore struct {
	mu sync.RWMutex
	m  map[string]map[string][]byte
}

func (s *memStore) Put(_ context.Context, bucket, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[bucket]
	if !ok {
		b = map[string][]byte{}
		s.m[bucket] = b
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	b[key] = cp
	return nil
}

func (s *memStore) Get(_ context.Context, bucket, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[bucket]
	if !ok {
		return nil, ErrNotFound
	}
	v, ok := b[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (s *memStore) Delete(_ context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.m[bucket]; ok {
		delete(b, key)
	}
	return nil
}

func (s *memStore) List(_ context.Context, bucket string) ([]KV, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[bucket]
	if !ok {
		return nil, nil
	}
	out := make([]KV, 0, len(b))
	for k, v := range b {
		cp := make([]byte, len(v))
		copy(cp, v)
		out = append(out, KV{Key: k, Value: cp})
	}
	// stable order
	sortKV(out)
	return out, nil
}

func (s *memStore) Close() error { return nil }

// OpenBolt opens a BoltDB-backed Store at the given path with a 5s open
// timeout (matches protocol-daemon's choice).
func OpenBolt(path string) (Store, error) {
	if path == "" {
		return nil, errors.New("store: empty bolt path")
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bolt %s: %w", path, err)
	}
	return &boltStore{db: db}, nil
}

type boltStore struct{ db *bbolt.DB }

func (s *boltStore) Put(_ context.Context, bucket, key string, value []byte) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	})
}

func (s *boltStore) Get(_ context.Context, bucket, key string) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return ErrNotFound
		}
		v := b.Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		out = make([]byte, len(v))
		copy(out, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *boltStore) Delete(_ context.Context, bucket, key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

func (s *boltStore) List(_ context.Context, bucket string) ([]KV, error) {
	var out []KV
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			cp := make([]byte, len(v))
			copy(cp, v)
			out = append(out, KV{Key: string(k), Value: cp})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *boltStore) Close() error { return s.db.Close() }

// sortKV sorts a KV slice by Key in place using a simple insertion sort.
// Avoids importing sort just for tests; n is small in practice.
func sortKV(kv []KV) {
	for i := 1; i < len(kv); i++ {
		for j := i; j > 0 && kv[j-1].Key > kv[j].Key; j-- {
			kv[j-1], kv[j] = kv[j], kv[j-1]
		}
	}
}
