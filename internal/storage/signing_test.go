package storage

import (
	"strconv"
	"testing"
	"time"
)

// newTestGCSStore constructs a GCSStore directly (bypassing NewGCSStore,
// which dials the real GCS client and needs network/credentials this test
// must not depend on) purely to exercise its pure, network-free
// VerifySignedURL/SignedURL logic. Same-package white-box test only —
// signingSecret and Now are unexported.
func newTestGCSStore(secret string, now func() time.Time) *GCSStore {
	return &GCSStore{signingSecret: secret, Now: now}
}

// TestSignedURLVerificationIsBackendIndependent pins the spec gap this
// branch closes: LocalStore and GCSStore must judge a given
// (objectPath, exp, sig) triple identically, so a client's signed URL is
// honoured (or rejected) the same way no matter which storage backend the
// deployment is configured with. If either backend's VerifySignedURL is
// ever reimplemented with its own bespoke HMAC/expiry logic instead of
// sharing verifySignature (signing.go), this test starts asserting two
// different booleans for the same input and fails immediately.
func TestSignedURLVerificationIsBackendIndependent(t *testing.T) {
	const secret = "shared-signing-secret-for-both-backends"
	fixedNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	local, err := NewLocalStore(t.TempDir(), "http://cdn.test", secret)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	local.Now = clock
	gcs := newTestGCSStore(secret, clock)

	validExp := strconv.FormatInt(fixedNow.Add(time.Minute).Unix(), 10)
	expiredExp := strconv.FormatInt(fixedNow.Add(-time.Minute).Unix(), 10)
	validSig := signObjectPath(secret, "plugins/p/versions/1.0.0/p-1.0.0.jar", validExp)

	cases := []struct {
		name       string
		objectPath string
		exp        string
		sig        string
		want       bool
	}{
		{"valid signature, not yet expired", "plugins/p/versions/1.0.0/p-1.0.0.jar", validExp, validSig, true},
		{"expired exp", "plugins/p/versions/1.0.0/p-1.0.0.jar", expiredExp, signObjectPath(secret, "plugins/p/versions/1.0.0/p-1.0.0.jar", expiredExp), false},
		{"wrong signature", "plugins/p/versions/1.0.0/p-1.0.0.jar", validExp, "deadbeef", false},
		{"signature for a different object path", "plugins/other/versions/1.0.0/other-1.0.0.jar", validExp, validSig, false},
		{"malformed exp", "plugins/p/versions/1.0.0/p-1.0.0.jar", "not-a-number", validSig, false},
		{"empty signature", "plugins/p/versions/1.0.0/p-1.0.0.jar", validExp, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotLocal := local.VerifySignedURL(c.objectPath, c.exp, c.sig)
			gotGCS := gcs.VerifySignedURL(c.objectPath, c.exp, c.sig)
			if gotLocal != c.want {
				t.Errorf("LocalStore.VerifySignedURL = %v, want %v", gotLocal, c.want)
			}
			if gotGCS != c.want {
				t.Errorf("GCSStore.VerifySignedURL = %v, want %v", gotGCS, c.want)
			}
			if gotLocal != gotGCS {
				t.Errorf("backends disagree: LocalStore=%v GCSStore=%v for the same input — signed-URL enforcement is no longer storage-backend-independent", gotLocal, gotGCS)
			}
		})
	}
}

// TestVerifySignedURLUsesInjectedClockNotWallTime proves the expiry check is
// judged against the injected Now, not time.Now(), on both backends: an exp
// timestamp that is already in the past by the real wall clock still
// verifies as valid when the injected clock is itself even further in the
// past (before that exp), and an exp in the real future still fails once
// the injected clock is moved past it — neither of which a wall-clock-only
// implementation could ever do without sleeping.
func TestVerifySignedURLUsesInjectedClockNotWallTime(t *testing.T) {
	const secret = "clock-test-secret-32-bytes-long!"
	const objectPath = "plugins/p/versions/1.0.0/p-1.0.0.jar"

	// exp is far in the real past.
	longAgo := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	exp := strconv.FormatInt(longAgo.Unix(), 10)
	sig := signObjectPath(secret, objectPath, exp)

	local, err := NewLocalStore(t.TempDir(), "http://cdn.test", secret)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	gcs := newTestGCSStore(secret, nil)

	// Before the boundary (clock still earlier than exp): valid.
	before := longAgo.Add(-time.Second)
	local.Now = func() time.Time { return before }
	gcs.Now = func() time.Time { return before }
	if !local.VerifySignedURL(objectPath, exp, sig) {
		t.Fatalf("LocalStore: expected valid when injected clock is before exp, regardless of real wall time")
	}
	if !gcs.VerifySignedURL(objectPath, exp, sig) {
		t.Fatalf("GCSStore: expected valid when injected clock is before exp, regardless of real wall time")
	}

	// Exactly at the boundary: valid (">" is the rejection condition, not "==").
	atBoundary := longAgo
	local.Now = func() time.Time { return atBoundary }
	gcs.Now = func() time.Time { return atBoundary }
	if !local.VerifySignedURL(objectPath, exp, sig) {
		t.Fatalf("LocalStore: expected valid exactly at the expiry boundary")
	}
	if !gcs.VerifySignedURL(objectPath, exp, sig) {
		t.Fatalf("GCSStore: expected valid exactly at the expiry boundary")
	}

	// After the boundary: invalid.
	after := longAgo.Add(time.Second)
	local.Now = func() time.Time { return after }
	gcs.Now = func() time.Time { return after }
	if local.VerifySignedURL(objectPath, exp, sig) {
		t.Fatalf("LocalStore: expected invalid once the injected clock passes exp")
	}
	if gcs.VerifySignedURL(objectPath, exp, sig) {
		t.Fatalf("GCSStore: expected invalid once the injected clock passes exp")
	}
}
