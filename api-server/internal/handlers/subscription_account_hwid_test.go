package handlers

import "testing"

func TestValidateHWID(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"abcdefghij", true},                          // exactly 10 chars
		{"a1B2c3D4e5F6g7H8i9J0", true},               // 20 chars mixed
		{"a!@#$%^&*()", true},                         // printable ASCII symbols
		{string(make([]byte, 64)), false},             // 64 NUL bytes – not printable
		{"abcdefghi", false},                          // 9 chars – too short
		{"", false},                                   // empty
		{"abcdefghij klm", false},                     // contains space
		{"abcdefghij\t", false},                       // contains tab
		{string([]byte{0x01, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i'}), false}, // non-printable
		// exactly 64 printable ASCII chars: valid
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@", true},
		// 65 chars: too long
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@X", false},
	} {
		_, got := validateHWID(tc.input)
		if got != tc.want {
			t.Errorf("validateHWID(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestHWIDFingerprintDeterministic(t *testing.T) {
	pepper := "test-pepper-value"
	hwid := "client-install-id-1"
	fp1 := hwidFingerprint(pepper, hwid)
	fp2 := hwidFingerprint(pepper, hwid)
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q != %q", fp1, fp2)
	}
	if len(fp1) != 64 {
		t.Errorf("expected 64-char hex fingerprint, got %d", len(fp1))
	}
}

func TestHWIDFingerprintDifferentPeppers(t *testing.T) {
	hwid := "same-client-id"
	fp1 := hwidFingerprint("pepper-a", hwid)
	fp2 := hwidFingerprint("pepper-b", hwid)
	if fp1 == fp2 {
		t.Error("different peppers should produce different fingerprints")
	}
}

func TestHWIDFingerprintDifferentHWIDs(t *testing.T) {
	pepper := "shared-pepper"
	fp1 := hwidFingerprint(pepper, "client-id-one")
	fp2 := hwidFingerprint(pepper, "client-id-two")
	if fp1 == fp2 {
		t.Error("different HWIDs should produce different fingerprints")
	}
}
