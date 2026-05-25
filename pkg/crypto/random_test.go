package crypto

import "testing"

func TestGenerateRandomString_Length(t *testing.T) {
	for _, length := range []int{8, 16, 32, 64} {
		s := GenerateRandomString(length)
		if len(s) != length {
			t.Errorf("GenerateRandomString(%d) length = %d", length, len(s))
		}
	}
}

func TestGenerateRandomString_Unique(t *testing.T) {
	a := GenerateRandomString(32)
	b := GenerateRandomString(32)
	if a == b {
		t.Error("GenerateRandomString() generated duplicate strings")
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	pw := GenerateRandomPassword()
	if len(pw) != 16 {
		t.Errorf("GenerateRandomPassword() length = %d, want 16", len(pw))
	}
}

func TestGenerateRandomPassword_Unique(t *testing.T) {
	a := GenerateRandomPassword()
	b := GenerateRandomPassword()
	if a == b {
		t.Error("GenerateRandomPassword() generated duplicate passwords")
	}
}
