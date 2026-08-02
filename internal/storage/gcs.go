package storage

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type GCSStore struct {
	client        *storage.Client
	bucket        string
	privateBucket string
	cdnBase       string
	signingSecret string

	// Now returns the current time. nil (the production default) means
	// time.Now().UTC(). See LocalStore.Now / LocalStore.now — both backends
	// share the same convention and the same underlying verifySignature so
	// signed-URL expiry is enforced identically regardless of which one is
	// configured (internal/httpapi's /cdn edge doesn't special-case either).
	Now func() time.Time
}

func (s *GCSStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func NewGCSStore(ctx context.Context, bucket, privateBucket, cdnBase, signingSecret string) (*GCSStore, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCSStore{
		client:        client,
		bucket:        bucket,
		privateBucket: privateBucket,
		cdnBase:       strings.TrimRight(cdnBase, "/"),
		signingSecret: signingSecret,
	}, nil
}

func (s *GCSStore) Type() string { return "gcs" }

func (s *GCSStore) bucketFor(objectPath string) string {
	if s.privateBucket != "" && IsPrivateObject(objectPath) {
		return s.privateBucket
	}
	return s.bucket
}

func (s *GCSStore) obj(objectPath string) *storage.ObjectHandle {
	return s.client.Bucket(s.bucketFor(objectPath)).Object(objectPath)
}

func (s *GCSStore) Read(ctx context.Context, objectPath string) (*Object, error) {
	reader, err := s.obj(objectPath).Generation(0).NewReader(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return &Object{Path: objectPath, Data: data, Generation: reader.Attrs.Generation}, nil
}

func (s *GCSStore) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	w := s.obj(objectPath).If(storage.Conditions{DoesNotExist: expectedGen == 0}).NewWriter(ctx)
	if expectedGen > 0 {
		w = s.obj(objectPath).If(storage.Conditions{GenerationMatch: expectedGen}).NewWriter(ctx)
	}
	w.ContentType = "application/octet-stream"
	if _, err := w.Write(data); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		if strings.Contains(err.Error(), "condition") {
			return 0, ErrConflict
		}
		return 0, err
	}
	attrs, err := s.obj(objectPath).Attrs(ctx)
	if err != nil {
		return 0, err
	}
	return attrs.Generation, nil
}

func (s *GCSStore) Delete(ctx context.Context, objectPath string) error {
	if err := s.obj(objectPath).Delete(ctx); err != nil {
		if err == storage.ErrObjectNotExist {
			return nil
		}
		return err
	}
	return nil
}

func (s *GCSStore) Stat(ctx context.Context, objectPath string) (*ObjectMeta, error) {
	attrs, err := s.obj(objectPath).Attrs(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &ObjectMeta{Path: objectPath, Size: attrs.Size, Generation: attrs.Generation, CreatedAt: attrs.Created}, nil
}

func (s *GCSStore) Exists(ctx context.Context, objectPath string) (bool, error) {
	_, err := s.obj(objectPath).Attrs(ctx)
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	return err == nil, err
}

func (s *GCSStore) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	out, err := s.listBucketPrefix(ctx, s.bucket, prefix)
	if err != nil {
		return nil, err
	}
	if s.privateBucket != "" && s.privateBucket != s.bucket {
		priv, err := s.listBucketPrefix(ctx, s.privateBucket, prefix)
		if err != nil {
			return nil, err
		}
		out = append(out, priv...)
	}
	return out, nil
}

func (s *GCSStore) listBucketPrefix(ctx context.Context, bucket, prefix string) ([]string, error) {
	it := s.client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	var out []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, attrs.Name)
	}
	return out, nil
}

func (s *GCSStore) PublicURL(objectPath string) string {
	return s.cdnBase + "/" + CDNPath(objectPath)
}

func (s *GCSStore) SignedURL(ctx context.Context, objectPath string, ttl time.Duration) (string, time.Time, error) {
	if s.signingSecret == "" {
		return "", time.Time{}, fmt.Errorf("STORAGE_SIGNING_SECRET is required for signed URLs")
	}
	expiresAt := s.now().Add(ttl)
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	sig := signObjectPath(s.signingSecret, objectPath, exp)
	url := fmt.Sprintf("%s/%s?exp=%s&sig=%s", s.cdnBase, CDNPath(objectPath), exp, sig)
	return url, expiresAt, nil
}

// VerifySignedURL reports whether sig is a valid, unexpired signature for
// objectPath. It shares its implementation (verifySignature) with
// LocalStore.VerifySignedURL byte-for-byte — see that function's doc
// comment for why the two backends deliberately cannot diverge here.
func (s *GCSStore) VerifySignedURL(objectPath, exp, sig string) bool {
	return verifySignature(s.signingSecret, objectPath, exp, sig, s.now())
}

func (s *GCSStore) Ready(ctx context.Context) error {
	if s.bucket == "" {
		return ErrUnavailable
	}
	attrs, err := s.client.Bucket(s.bucket).Attrs(ctx)
	if err != nil {
		return err
	}
	if attrs == nil {
		return ErrUnavailable
	}
	return nil
}
