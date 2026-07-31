package storage

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestLocalStoreConcurrentWrite(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root, "http://localhost/cdn", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := "test/object.json"

	var wg sync.WaitGroup
	successes := make(chan int64, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			gen, err := store.Write(ctx, path, []byte("data"), 0)
			if err == nil {
				successes <- gen
			}
			_ = n
		}(i)
	}
	wg.Wait()
	close(successes)

	var count int
	var lastGen int64
	for g := range successes {
		count++
		if g > lastGen {
			lastGen = g
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one successful first write, got %d", count)
	}

	gen, err := store.Write(ctx, path, []byte("updated"), lastGen)
	if err != nil {
		t.Fatalf("expected successful CAS write: %v", err)
	}
	if gen != lastGen+1 {
		t.Fatalf("expected generation increment, got %d", gen)
	}

	_, err = store.Write(ctx, path, []byte("stale"), lastGen)
	if err != ErrConflict {
		t.Fatalf("expected conflict, got %v", err)
	}

	obj, err := store.Read(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Data) != "updated" {
		t.Fatalf("unexpected data: %s", obj.Data)
	}

	if err := store.Delete(ctx, path); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, path)
	if err != ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}

	genPath := filepath.Join(root, "test", "object.json.gen")
	if _, err := store.Write(ctx, "other", []byte("x"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	_ = genPath
}
