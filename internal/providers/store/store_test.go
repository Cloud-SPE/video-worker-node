package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type item struct{ A int }

func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Get(ctx, "b", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty: %v", err)
	}
	if err := s.Put(ctx, "b", "x", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "b", "x")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Fatalf("got=%q", got)
	}
	if err := PutJSON(ctx, s, "b", "j", item{A: 7}); err != nil {
		t.Fatal(err)
	}
	var v item
	if err := GetJSON(ctx, s, "b", "j", &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 7 {
		t.Fatalf("v.A=%d", v.A)
	}
	kvs, err := s.List(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(kvs) != 2 {
		t.Fatalf("kvs=%v", kvs)
	}
	// Sorted keys: j < x
	if kvs[0].Key != "j" || kvs[1].Key != "x" {
		t.Fatalf("order=%v", kvs)
	}
	if err := s.Delete(ctx, "b", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "b", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	// Listing absent bucket returns empty.
	if kvs, err := s.List(ctx, "ghost"); err != nil || len(kvs) != 0 {
		t.Fatalf("ghost list: kvs=%v err=%v", kvs, err)
	}
	// Delete on absent bucket is a no-op.
	if err := s.Delete(ctx, "ghost", "z"); err != nil {
		t.Fatalf("ghost delete: %v", err)
	}
}

func TestMemoryStore(t *testing.T) {
	t.Parallel()
	s := Memory()
	defer s.Close()
	runStoreContract(t, s)
}

func TestBoltStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenBolt(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	runStoreContract(t, s)
}

func TestOpenBoltEmpty(t *testing.T) {
	t.Parallel()
	if _, err := OpenBolt(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestGetJSONErrorPropagation(t *testing.T) {
	t.Parallel()
	s := Memory()
	defer s.Close()
	var v item
	if err := GetJSON(context.Background(), s, "b", "missing", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	// Bad json on a real put.
	if err := s.Put(context.Background(), "b", "k", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if err := GetJSON(context.Background(), s, "b", "k", &v); err == nil {
		t.Fatal("expected json unmarshal error")
	}
}
