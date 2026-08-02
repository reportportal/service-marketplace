package domain

import "testing"

// TestDeriveLicenseKeyID_KnownVector pins DeriveLicenseKeyID against an
// independently-computed value (Python's hashlib, not this package) so a regression to
// hashing the base64 TEXT instead of the decoded key bytes -- or truncating to the
// wrong length, or using the wrong hash -- is caught here rather than only showing up
// as a verification mismatch three layers away.
//
//	>>> import base64, hashlib
//	>>> raw = base64.b64decode("3q2+7w==")   # 0xDE 0xAD 0xBE 0xEF
//	>>> hashlib.sha256(raw).hexdigest()[:8]
//	'5f78c332'
func TestDeriveLicenseKeyID_KnownVector(t *testing.T) {
	got, err := DeriveLicenseKeyID("3q2+7w==")
	if err != nil {
		t.Fatalf("DeriveLicenseKeyID: %v", err)
	}
	if got != "5f78c332" {
		t.Fatalf("DeriveLicenseKeyID(%q) = %q, want %q", "3q2+7w==", got, "5f78c332")
	}
	if len(got) != 8 {
		t.Fatalf("DeriveLicenseKeyID length = %d, want 8", len(got))
	}
}

func TestDeriveLicenseKeyID_InvalidBase64(t *testing.T) {
	if _, err := DeriveLicenseKeyID("not-valid-base64!!!"); err == nil {
		t.Fatalf("DeriveLicenseKeyID: want error for invalid base64 input, got nil")
	}
}

// TestLicensePublicKey_ResolvedKeyID_IgnoresStoredField proves the authoritative-
// derivation guarantee: a stored KeyID that disagrees with (or predates) what the key
// bytes actually hash to must never be trusted by a verifier. This is the exact shape
// of a document written before the KeyID field existed (KeyID == ""), and of a
// hypothetically corrupted/stale stored value -- both must resolve to the same,
// re-derived id.
func TestLicensePublicKey_ResolvedKeyID_IgnoresStoredField(t *testing.T) {
	want, err := DeriveLicenseKeyID("3q2+7w==")
	if err != nil {
		t.Fatalf("DeriveLicenseKeyID: %v", err)
	}

	cases := []struct {
		name   string
		stored string
	}{
		{"empty (legacy document)", ""},
		{"present but wrong", "deadbeef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := LicensePublicKey{KeyID: c.stored, PublicKey: "3q2+7w=="}
			got, err := k.ResolvedKeyID()
			if err != nil {
				t.Fatalf("ResolvedKeyID: %v", err)
			}
			if got != want {
				t.Fatalf("ResolvedKeyID() = %q, want %q (derived from PublicKey, not the stored field)", got, want)
			}
		})
	}
}
