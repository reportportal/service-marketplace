package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// signObjectPath computes the hex HMAC-SHA256 signature LocalStore and
// GCSStore both attach to a signed URL: HMAC(secret, objectPath+"|"+exp).
// It is the single place that math is written, so the two backends cannot
// silently diverge — see verifySignature's doc comment for why that
// equivalence matters at the /cdn edge.
func signObjectPath(secret, objectPath, exp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(objectPath + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifySignature reports whether sig is a valid, unexpired signature for
// objectPath, judged against now (never wall-clock time directly — callers
// pass their own now func() time.Time, nil-safe-defaulted to time.Now().UTC(),
// so the expiry boundary is testable without sleeping).
//
// This is the ONE verification routine LocalStore.VerifySignedURL and
// GCSStore.VerifySignedURL both call: internal/httpapi's /cdn edge is meant
// to enforce signed-URL expiry and signature identically regardless of
// storage backend (the spec gap this closes), and the only way to guarantee
// that going forward is for both backends to share the same code path
// rather than two hand-kept-in-sync copies of the same HMAC math. See
// TestSignedURLVerificationIsBackendIndependent (signing_test.go), which
// fails if LocalStore and GCSStore are ever given their own bespoke
// implementations again.
func verifySignature(secret, objectPath, exp, sig string, now time.Time) bool {
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > expUnix {
		return false
	}
	expected := signObjectPath(secret, objectPath, exp)
	// hmac.Equal, not ==: this comparison is against an attacker-controlled
	// sig, so it must run in constant time regardless of how many leading
	// bytes match, or the comparison itself becomes a timing side-channel an
	// attacker can use to recover a valid signature byte-by-byte. Do not
	// "simplify" this to expected == sig.
	return hmac.Equal([]byte(expected), []byte(sig))
}

// SignObjectPath is the exported form of signObjectPath. Its only reason to
// exist is so a storage.ObjectStore test double outside this package (see
// internal/httpapi's fakeObjectStore) can produce a wire-compatible signed
// URL by calling the real signing code instead of hand-rolling its own copy
// of the HMAC math — a double that reimplements the thing under test cannot
// catch that thing's bugs. Production callers within this package continue
// to use the unexported signObjectPath directly.
func SignObjectPath(secret, objectPath, exp string) string {
	return signObjectPath(secret, objectPath, exp)
}

// VerifySignature is the exported form of verifySignature, for the same
// reason as SignObjectPath above: it lets a test double share the real
// verification path (constant-time comparison, expiry boundary and all)
// instead of reimplementing it.
func VerifySignature(secret, objectPath, exp, sig string, now time.Time) bool {
	return verifySignature(secret, objectPath, exp, sig, now)
}
