package storage

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

// TestLocalStoreReadIsAtomicWithGeneration proves that Read() cannot return a
// (data, generation) pair that never existed together. It engineers a
// deterministic interleaving -- not a race that merely might get caught --
// by parking a Read() call at the exact seam between its byte-read and its
// generation-read, running a complete concurrent Write() across that seam,
// and then resuming the Read().
//
// A Read() call that started before the Write() began must observe the
// pre-write snapshot in its entirety: generation g1 paired with the bytes
// that were live at g1. Anything else -- in particular the post-write
// generation paired with the pre-write bytes -- is the torn read this test
// exists to catch.
func TestLocalStoreReadIsAtomicWithGeneration(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root, "http://localhost/cdn", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := "test/object.json"

	gen1, err := store.Write(ctx, path, []byte("v1"), 0)
	if err != nil {
		t.Fatalf("seed write failed: %v", err)
	}

	readerAtSeam := make(chan struct{})
	resumeReader := make(chan struct{})
	store.testAfterReadData = func() {
		close(readerAtSeam)
		<-resumeReader
	}

	writerCommitted := make(chan struct{})
	store.testAfterWriteCommit = func() {
		close(writerCommitted)
	}

	type readResult struct {
		obj *Object
		err error
	}
	readDone := make(chan readResult, 1)
	go func() {
		obj, err := store.Read(ctx, path)
		readDone <- readResult{obj, err}
	}()

	<-readerAtSeam // Read() has the pre-write bytes and is paused before resolving the generation.

	type writeResult struct {
		gen int64
		err error
	}
	writeDone := make(chan writeResult, 1)
	go func() {
		gen, err := store.Write(ctx, path, []byte("v2"), gen1)
		writeDone <- writeResult{gen, err}
	}()

	// The Write must either fully commit here (proving Read released the lock
	// mid-operation, the bug) or remain blocked behind the lock the paused
	// Read still holds (the fix). This wait only decides which of those two
	// worlds we are in; the correctness assertion below does not depend on
	// which branch fires.
	select {
	case <-writerCommitted:
	case <-time.After(500 * time.Millisecond):
	}

	close(resumeReader)

	wres := <-writeDone
	if wres.err != nil {
		t.Fatalf("concurrent write failed: %v", wres.err)
	}
	if wres.gen != gen1+1 {
		t.Fatalf("expected write to land at generation %d, got %d", gen1+1, wres.gen)
	}

	rres := <-readDone
	if rres.err != nil {
		t.Fatalf("read failed: %v", rres.err)
	}

	// Read()'s byte-read completed before the concurrent Write() even started
	// (we only launched the writer after observing readerAtSeam). A correct,
	// atomic Read() must therefore report the pre-write snapshot in full.
	if rres.obj.Generation != gen1 || string(rres.obj.Data) != "v1" {
		t.Fatalf("torn read: got (generation=%d, data=%q), want (generation=%d, data=%q) -- "+
			"Read() paired bytes from one point in time with a generation from another",
			rres.obj.Generation, rres.obj.Data, gen1, "v1")
	}
}

func TestLocalStoreRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root, "http://localhost/cdn", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, p := range []string{"../etc/passwd", "..\\windows", "/etc/passwd", "plugins/../../outside"} {
		if _, err := store.Read(ctx, p); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound for %q, got %v", p, err)
		}
	}
}

// TestLocalStoreStat_ReturnsCreationTimeAndGenerationWithoutBody proves Stat
// gives the AMD-27 orphan-cleanup age guard (internal/lifecycle.OrphanCleanup)
// what it needs -- an object's creation time and current generation -- without
// requiring a full Read() of a potentially large jar just to learn its age.
// If Stat's CreatedAt is left zero-valued, or is a snapshot fixed at open time
// instead of tracking the object's real mtime, the age guard would either hold
// every orphan forever (a zero time reads as "just written" against
// time.Now().Sub) or never re-observe a rewrite.
func TestLocalStoreStat_ReturnsCreationTimeAndGenerationWithoutBody(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root, "http://localhost/cdn", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := "plugins/p/versions/1.0.0/p-1.0.0.jar"

	before := time.Now().Add(-time.Second)
	gen1, err := store.Write(ctx, path, []byte("jar-bytes"), 0)
	if err != nil {
		t.Fatalf("seed write: %v", err)
	}
	after := time.Now().Add(time.Second)

	meta, err := store.Stat(ctx, path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if meta.Generation != gen1 {
		t.Fatalf("Generation = %d, want %d", meta.Generation, gen1)
	}
	if meta.CreatedAt.Before(before) || meta.CreatedAt.After(after) {
		t.Fatalf("CreatedAt = %v, want between %v and %v", meta.CreatedAt, before, after)
	}
	if meta.Size != int64(len("jar-bytes")) {
		t.Fatalf("Size = %d, want %d", meta.Size, len("jar-bytes"))
	}

	rewriteAfter := time.Now().Add(-time.Second)
	if _, err := store.Write(ctx, path, []byte("jar-bytes-v2"), gen1); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	meta2, err := store.Stat(ctx, path)
	if err != nil {
		t.Fatalf("Stat after rewrite: %v", err)
	}
	if !meta2.CreatedAt.After(rewriteAfter) {
		t.Fatalf("CreatedAt did not advance on rewrite: got %v, want after %v", meta2.CreatedAt, rewriteAfter)
	}

	if _, err := store.Stat(ctx, "plugins/missing/versions/1.0.0/missing.jar"); err != ErrNotFound {
		t.Fatalf("Stat on missing object: got %v, want ErrNotFound", err)
	}
}

func TestSanitizeScreenshotFilename(t *testing.T) {
	if _, err := SanitizeScreenshotFilename("../../authorized_keys.json"); err == nil {
		t.Fatal("expected error")
	}
	safe, err := SanitizeScreenshotFilename("Shot.PNG")
	if err != nil {
		t.Fatal(err)
	}
	if safe != "shot.png" {
		t.Fatalf("got %s", safe)
	}
}
