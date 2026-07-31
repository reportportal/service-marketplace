package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	expiresAt := time.Now().UTC().Add(ttl)
	if s.signingSecret == "" {
		return "", time.Time{}, fmt.Errorf("STORAGE_SIGNING_SECRET is required for signed URLs")
	}
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.signingSecret))
	_, _ = mac.Write([]byte(objectPath + "|" + exp))
	sig := hex.EncodeToString(mac.Sum(nil))
	url := fmt.Sprintf("%s/%s?exp=%s&sig=%s", s.cdnBase, CDNPath(objectPath), exp, sig)
	return url, expiresAt, nil
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
