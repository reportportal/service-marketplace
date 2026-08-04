package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

func signObjectPath(secret, objectPath, exp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(objectPath + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

func verifySignature(secret, objectPath, exp, sig string, now time.Time) bool {
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > expUnix {
		return false
	}
	expected := signObjectPath(secret, objectPath, exp)
	return hmac.Equal([]byte(expected), []byte(sig))
}

// SignObjectPath / VerifySignature export the shared HMAC helpers for tests and doubles.
func SignObjectPath(secret, objectPath, exp string) string {
	return signObjectPath(secret, objectPath, exp)
}

func VerifySignature(secret, objectPath, exp, sig string, now time.Time) bool {
	return verifySignature(secret, objectPath, exp, sig, now)
}
