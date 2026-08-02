package license

// Tests for the two remaining pieces of chunk 2/3's mandate that verify_test.go's
// single-goroutine tests cannot exercise:
//
//   - RevokeKey's optimistic-concurrency (CAS) discipline under real concurrent
//     writers to the same authorized_keys.json document -- a naive read-modify-write
//     would silently lose one of two concurrent revocations.
//   - AMD-25's 30-second revocation-propagation bound. Service.VerifyToken has no key
//     cache of its own (every call re-reads storage via Service.load) -- this test
//     PINS that fact so a future cache added here can never silently exceed the
//     30-second bound. See the doc comment on TestVerifyToken_RevocationTakesEffectImmediately
//     for what "pin" means here and why no cache was added to satisfy AMD-25.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"sync"
	"testing"
	"time"
)

// TestRevokeKey_ConcurrentRevocations_BothApplied is item 1's explicit requirement:
// "two concurrent revocations must not lose one." It creates an entitlement with three
// live keys and revokes two of them (the third stays live throughout, so neither call
// can ever legitimately hit AMD-11's "last active key" 422 guard) from two goroutines
// racing against the same storage document, relying on RevokeKey's use of
// storage.WriteWithRetry (real generation-based CAS against storage.LocalStore, not a
// mock) to make both writes land instead of one silently clobbering the other.
func TestRevokeKey_ConcurrentRevocations_BothApplied(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()

	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.RotateKey(ctx, "acme-corp"); err != nil {
		t.Fatalf("RotateKey (2nd key): %v", err)
	}
	if _, err := svc.RotateKey(ctx, "acme-corp"); err != nil {
		t.Fatalf("RotateKey (3rd key): %v", err)
	}
	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ents[0].PublicKeys) != 3 {
		t.Fatalf("want 3 public keys, got %d", len(ents[0].PublicKeys))
	}
	keyA := ents[0].PublicKeys[0].KeyID
	keyB := ents[0].PublicKeys[1].KeyID
	keyC := ents[0].PublicKeys[2].KeyID // left live throughout; never revoked here.

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, keyID := range []string{keyA, keyB} {
		wg.Add(1)
		go func(keyID string) {
			defer wg.Done()
			<-start // maximize the chance both goroutines race the same read generation
			errs <- svc.RevokeKey(ctx, "acme-corp", keyID)
		}(keyID)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RevokeKey returned an error -- a lost/conflicting update was not retried away: %v", err)
		}
	}

	ents, err = svc.List(ctx)
	if err != nil {
		t.Fatalf("List after concurrent revokes: %v", err)
	}
	revoked := map[string]bool{}
	for _, k := range ents[0].PublicKeys {
		revoked[k.KeyID] = k.RevokedAt != nil
	}
	if !revoked[keyA] {
		t.Fatalf("key A (%s) was not revoked -- concurrent update lost", keyA)
	}
	if !revoked[keyB] {
		t.Fatalf("key B (%s) was not revoked -- concurrent update lost", keyB)
	}
	if revoked[keyC] {
		t.Fatalf("key C (%s) was unexpectedly revoked", keyC)
	}
}

// TestVerifyToken_RevocationTakesEffectImmediately is AMD-25's propagation bound (at
// most 30 seconds) pinned at its tightest possible margin: zero elapsed clock time.
// Service.VerifyToken calls Service.load, which reads storage.PathAuthorizedKeys fresh
// on every call -- there is no key cache anywhere in this package today. This test
// exists precisely so that changes to come (if this package ever grows a cache in
// front of that read) cannot silently widen the propagation window past 30s without
// this test going red first: it revokes the signing key and re-verifies the exact same
// token at the exact same instant (svc.Now is never advanced between the calls), so
// ANY caching of the pre-revocation key list -- even one bounded well inside 30s --
// would make the second VerifyToken call wrongly succeed.
func TestVerifyToken_RevocationTakesEffectImmediately(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	ctx := context.Background()

	res, err := svc.Create(ctx, "acme-corp", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A second live key so RevokeKey below isn't refused as the entitlement's last
	// active key (AMD-11's unrelated 422 guard, already covered elsewhere).
	if _, err := svc.RotateKey(ctx, "acme-corp"); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keyID := ents[0].PublicKeys[0].KeyID

	priv, err := base64.StdEncoding.DecodeString(res.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	k := verifyTestKey{priv: ed25519.PrivateKey(priv)}
	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour))

	// Sanity: the token verifies before revocation.
	if _, err := svc.VerifyToken(ctx, token); err != nil {
		t.Fatalf("VerifyToken before revocation: %v", err)
	}

	if err := svc.RevokeKey(ctx, "acme-corp", keyID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	// Zero elapsed time -- svc.Now is unchanged. AMD-25 allows up to 30s; this proves
	// the actual bound today is 0s, not merely "eventually within 30s".
	if _, err := svc.VerifyToken(ctx, token); err == nil {
		t.Fatalf("token signed by a just-revoked key verified with zero elapsed time -- AMD-25 requires revocation within 30s, but this proves some layer is caching stale (pre-revocation) key state")
	}
}
