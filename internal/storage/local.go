package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LocalStore struct {
	root          string
	cdnBase       string
	signingSecret string
	mu            sync.RWMutex
	generations   map[string]int64

	// Now returns the current time. nil (the production default) means
	// time.Now().UTC() — see the now() helper. A test sets this field
	// directly to move the signed-URL expiry boundary without sleeping,
	// mirroring internal/lifecycle.OrphanCleanup's Config.Now.
	Now func() time.Time

	// testAfterReadData and testAfterWriteCommit are test-only seams, nil in
	// production. They let tests deterministically pause Read between its
	// byte-read and generation-read steps, and observe the instant a Write
	// becomes durable, in order to prove the two cannot interleave.
	testAfterReadData    func()
	testAfterWriteCommit func()
}

// now returns s.Now() if set, else time.Now().UTC() — the same nil-safe
// fallback convention internal/lifecycle.OrphanCleanup.Config.now uses.
func (s *LocalStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func NewLocalStore(root, cdnBase, signingSecret string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s := &LocalStore{
		root:          root,
		cdnBase:       strings.TrimRight(cdnBase, "/"),
		signingSecret: signingSecret,
		generations:   map[string]int64{},
	}
	if err := s.loadGenerations(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *LocalStore) Type() string { return "local" }

func (s *LocalStore) abs(objectPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(objectPath))
	clean = strings.TrimPrefix(clean, string(filepath.Separator))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return "", ErrNotFound
	}
	full := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, full)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", ErrNotFound
	}
	return full, nil
}

func (s *LocalStore) genPath(objectPath string) (string, error) {
	p, err := s.abs(objectPath)
	if err != nil {
		return "", err
	}
	return p + ".gen", nil
}

func (s *LocalStore) loadGenerations() error {
	return filepath.Walk(s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".gen") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		gen, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, strings.TrimSuffix(path, ".gen"))
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, "..") {
			return nil
		}
		key := filepath.ToSlash(rel)
		s.generations[key] = gen
		return nil
	})
}

func (s *LocalStore) setGeneration(objectPath string, gen int64) error {
	s.mu.Lock()
	s.generations[objectPath] = gen
	s.mu.Unlock()
	gp, err := s.genPath(objectPath)
	if err != nil {
		return err
	}
	return os.WriteFile(gp, []byte(strconv.FormatInt(gen, 10)), 0o644)
}

func (s *LocalStore) Read(ctx context.Context, objectPath string) (*Object, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	p, err := s.abs(objectPath)
	if err != nil {
		return nil, err
	}

	// The byte-read and the generation lookup must be a single atomic
	// operation from Write()'s point of view: Write() holds s.mu for its
	// entire critical section (rename + generation bump), so holding the
	// same RLock across both of our steps here guarantees Read() can only
	// ever observe a fully pre-write or fully post-write snapshot, never a
	// mix of the two.
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(p)
	if s.testAfterReadData != nil {
		s.testAfterReadData()
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Object{Path: objectPath, Data: data, Generation: s.generations[objectPath]}, nil
}

func (s *LocalStore) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	p, err := s.abs(objectPath)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.generations[objectPath]
	if expectedGen != current {
		return 0, ErrConflict
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return 0, err
	}

	next := current + 1
	s.generations[objectPath] = next
	gp := p + ".gen"
	if err := os.WriteFile(gp, []byte(strconv.FormatInt(next, 10)), 0o644); err != nil {
		return 0, err
	}
	if s.testAfterWriteCommit != nil {
		s.testAfterWriteCommit()
	}
	return next, nil
}

func (s *LocalStore) Delete(ctx context.Context, objectPath string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p, err := s.abs(objectPath)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(p + ".gen")
	s.mu.Lock()
	delete(s.generations, objectPath)
	s.mu.Unlock()
	return nil
}

func (s *LocalStore) Stat(ctx context.Context, objectPath string) (*ObjectMeta, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	p, err := s.abs(objectPath)
	if err != nil {
		return nil, err
	}

	// Same rationale as Read(): hold the lock across both the stat and the
	// generation lookup so this can never observe a torn pre/post-write mix.
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &ObjectMeta{Path: objectPath, Size: info.Size(), Generation: s.generations[objectPath], CreatedAt: info.ModTime()}, nil
}

func (s *LocalStore) Exists(ctx context.Context, objectPath string) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	p, err := s.abs(objectPath)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *LocalStore) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	base, err := s.abs(prefix)
	if err != nil {
		// empty prefix listing: use root
		if prefix == "" || prefix == "/" {
			base = s.root
		} else {
			return nil, err
		}
	}
	var out []string
	err = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() || strings.HasSuffix(path, ".gen") || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func (s *LocalStore) PublicURL(objectPath string) string {
	return s.cdnBase + "/" + CDNPath(objectPath)
}

func (s *LocalStore) SignedURL(ctx context.Context, objectPath string, ttl time.Duration) (string, time.Time, error) {
	expiresAt := s.now().Add(ttl)
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	sig := signObjectPath(s.signingSecret, objectPath, exp)
	url := fmt.Sprintf("%s/%s?exp=%s&sig=%s", s.cdnBase, CDNPath(objectPath), exp, sig)
	return url, expiresAt, nil
}

func (s *LocalStore) Ready(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	info, err := os.Stat(s.root)
	if err != nil {
		return ErrUnavailable
	}
	if !info.IsDir() {
		return ErrUnavailable
	}
	return nil
}

func (s *LocalStore) VerifySignedURL(objectPath, exp, sig string) bool {
	return verifySignature(s.signingSecret, objectPath, exp, sig, s.now())
}

func HashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func DecodeBase64Key(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
