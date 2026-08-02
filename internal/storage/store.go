package storage

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound           = errors.New("object not found")
	ErrConflict           = errors.New("storage conflict")
	ErrUnavailable        = errors.New("storage unavailable")
	ErrGenerationMismatch = errors.New("generation mismatch")
)

type Object struct {
	Path       string
	Data       []byte
	Generation int64
}

// ObjectMeta is an object's metadata without its body. It exists for callers
// that need to know how old (and how big) an object is without paying to
// read potentially large content into memory just to inspect its timestamp —
// the AMD-27 orphan-cleanup age guard (internal/lifecycle.OrphanCleanup)
// is the motivating caller.
type ObjectMeta struct {
	Path       string
	Size       int64
	Generation int64
	// CreatedAt is the backend's object-creation/last-write timestamp (GCS:
	// ObjectAttrs.Created; local: the file's mtime — set once at write time
	// and only ever advanced by a subsequent CAS overwrite, since objects
	// under plugins/**/versions/** are otherwise write-once).
	CreatedAt time.Time
}

type ObjectStore interface {
	Read(ctx context.Context, objectPath string) (*Object, error)
	Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error)
	Delete(ctx context.Context, objectPath string) error
	Exists(ctx context.Context, objectPath string) (bool, error)
	ListPrefix(ctx context.Context, prefix string) ([]string, error)
	// Stat returns objectPath's metadata without reading its body. Returns
	// ErrNotFound if the object does not exist.
	Stat(ctx context.Context, objectPath string) (*ObjectMeta, error)
	PublicURL(objectPath string) string
	SignedURL(ctx context.Context, objectPath string, ttl time.Duration) (url string, expiresAt time.Time, err error)
	Ready(ctx context.Context) error
	Type() string
}

func ReadJSON[T any](ctx context.Context, store ObjectStore, objectPath string, decode func([]byte) (T, error)) (T, int64, error) {
	var zero T
	obj, err := store.Read(ctx, objectPath)
	if err != nil {
		return zero, 0, err
	}
	v, err := decode(obj.Data)
	if err != nil {
		return zero, obj.Generation, err
	}
	return v, obj.Generation, nil
}

func WriteWithRetry(ctx context.Context, store ObjectStore, objectPath string, fn func([]byte, int64) ([]byte, error), maxAttempts int) error {
	var gen int64
	var existing []byte
	obj, err := store.Read(ctx, objectPath)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if obj != nil {
		gen = obj.Generation
		existing = obj.Data
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		next, err := fn(existing, gen)
		if err != nil {
			return err
		}
		_, err = store.Write(ctx, objectPath, next, gen)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrGenerationMismatch) {
			return err
		}
		obj, err = store.Read(ctx, objectPath)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if obj != nil {
			gen = obj.Generation
			existing = obj.Data
		} else {
			gen = 0
			existing = nil
		}
	}
	return ErrConflict
}
