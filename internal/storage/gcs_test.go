package storage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGCSStoreReadReturnsLiveObjectGeneration(t *testing.T) {
	const generation = "123456789"
	client, err := gcs.NewClient(context.Background(),
		gcs.WithJSONReads(),
		option.WithoutAuthentication(),
		option.WithHTTPClient(&http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Goog-Generation": []string{generation}},
				Body:       io.NopCloser(strings.NewReader(`{"plugins":[]}`)),
			}, nil
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store := &GCSStore{client: client, bucket: "marketplace"}
	obj, err := store.Read(context.Background(), PathIndex)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Generation != 123456789 {
		t.Fatalf("Read generation = %d, want 123456789", obj.Generation)
	}
}
