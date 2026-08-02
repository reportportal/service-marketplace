package storage

import (
	"errors"
	"strings"
	"testing"
)

// TestCanonicalizeObjectPath_RejectsReservedNamespaceAliases is the
// package-local contract test for the guard that internal/httpapi's router
// tests exercise end to end. It exists because deleting the alias rejection
// outright used to leave internal/storage entirely green: all the coverage
// lived in another package, so anyone refactoring this package in isolation
// (or extracting it) got no signal that they had removed a security guard.
//
// Mutation this kills: dropping the strings.EqualFold arm from
// CanonicalizeObjectPath's reserved-namespace loop.
func TestCanonicalizeObjectPath_RejectsReservedNamespaceAliases(t *testing.T) {
	for _, raw := range []string{
		"Auth/authorized_keys.json",
		"AUTH/AUTHORIZED_KEYS.JSON",
		"aUtH/authorized_keys.json",
		"Private/plugins/p/versions/1.0.0/p-1.0.0.jar",
		"PRIVATE/",
		"Auth",
		"/Auth/authorized_keys.json",
	} {
		if _, err := CanonicalizeObjectPath(raw); !errors.Is(err, ErrReservedNamespaceAlias) {
			t.Errorf("CanonicalizeObjectPath(%q) err = %v, want ErrReservedNamespaceAlias -- a case "+
				"alias of a reserved namespace resolves to the protected object on a "+
				"case-insensitive filesystem", raw, err)
		}
	}
}

// TestCanonicalizeObjectPath_RejectsWindowsSeparatorAndTrailingPunctuation
// closes the NTFS half of the aliasing class the case-alias fix names in its
// own doc comment but did not originally handle.
//
// On NTFS the Win32 layer treats '\' as a path separator and strips trailing
// dots and spaces from a component. CanonicalizeObjectPath splits the first
// segment on '/' only, so "auth\authorized_keys.json" is a SINGLE segment
// that is neither equal nor EqualFold-equal to "auth", and IsAuthObject's
// HasPrefix(p, "auth/") does not match it either -- yet LocalStore.abs uses
// filepath.FromSlash + filepath.Clean, which on Windows resolves it to
// <root>\auth\authorized_keys.json and returns the entitlement keys.
// "auth./authorized_keys.json" is the same story via the trailing dot.
//
// These spellings cannot be reproduced on macOS/APFS (verified: os.ReadFile
// of "auth." and "auth " both ENOENT there), which is exactly why the
// router-level tests could not see them. Rejecting the spellings at the
// canonicalisation boundary IS platform-independent, so it is testable
// here -- and no key this codebase produces ever contains a backslash or a
// trailing dot or space, so there is no compatibility cost.
//
// Mutation this kills: removing the backslash / trailing dot / trailing
// space rejections from CanonicalizeObjectPath.
func TestCanonicalizeObjectPath_RejectsWindowsSeparatorAndTrailingPunctuation(t *testing.T) {
	for _, raw := range []string{
		`auth\authorized_keys.json`,
		`Auth\authorized_keys.json`,
		`private\plugins\p\versions\1.0.0\p-1.0.0.jar`,
		"auth./authorized_keys.json",
		"auth /authorized_keys.json",
		"plugins/acme/versions/1.0.0/acme-1.0.0.jar.",
		"plugins/acme ",
	} {
		if _, err := CanonicalizeObjectPath(raw); err == nil {
			t.Errorf("CanonicalizeObjectPath(%q) err = nil, want a rejection -- on NTFS this aliases "+
				"a different object than the one the guards judge", raw)
		}
	}
}

// TestCanonicalizeObjectPath_AcceptsLegitimateKeys pins the other side, so
// the rejections above cannot pass by refusing everything. Version strings
// legitimately carry mixed case (SemVer pre-release identifiers are
// case-sensitive), and those must survive untouched.
func TestCanonicalizeObjectPath_AcceptsLegitimateKeys(t *testing.T) {
	for _, raw := range []string{
		"index.json",
		"plugins/acme/versions/1.0.0/acme-1.0.0.jar",
		"plugins/acme/versions/1.0.0-RC1/manifest.json",
		"plugins/acme/versions/1.0.0/screenshots/a.png",
		"private/plugins/acme/versions/1.0.0/acme-1.0.0.jar",
		"auth/authorized_keys.json",
	} {
		got, err := CanonicalizeObjectPath(raw)
		if err != nil {
			t.Errorf("CanonicalizeObjectPath(%q) err = %v, want nil", raw, err)
			continue
		}
		if got != strings.TrimPrefix(raw, "/") {
			t.Errorf("CanonicalizeObjectPath(%q) = %q -- canonicalisation must never REWRITE a key, "+
				"only accept or refuse it", raw, got)
		}
	}
}

// TestHasReservedPrefix_CoversBareRoot pins that the bare namespace root is
// itself reserved, not just paths beneath it: "auth" alone used to escape
// HasPrefix(p, "auth/") and reach the store.
//
// Mutation this kills: reverting hasReservedPrefix to a plain
// strings.HasPrefix(p, ns+"/").
func TestHasReservedPrefix_CoversBareRoot(t *testing.T) {
	for _, p := range []string{"auth", "private"} {
		if !IsPrivateObject(p) {
			t.Errorf("IsPrivateObject(%q) = false, want true (bare reserved root)", p)
		}
	}
	if !IsAuthObject("auth") {
		t.Errorf(`IsAuthObject("auth") = false, want true (bare reserved root)`)
	}
	if IsAuthObject("authx/y") {
		t.Errorf(`IsAuthObject("authx/y") = true, want false -- "authx" is not the "auth" namespace`)
	}
}
