package lang_test

import (
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/lang"
)

func TestCodeValid(t *testing.T) {
	tests := []struct {
		code lang.Code
		want bool
	}{
		{lang.Korean, true},
		{lang.English, true},
		{lang.Chinese, true},
		{lang.Unset, true},
		{lang.Code("jp"), false},
		{lang.Code("KO"), false},
		{lang.Code("ko-KR"), false},
	}
	for _, tt := range tests {
		if got := tt.code.Valid(); got != tt.want {
			t.Errorf("Code(%q).Valid() = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestCodeTranslatable(t *testing.T) {
	tests := []struct {
		code lang.Code
		want bool
	}{
		{lang.Korean, true},
		{lang.English, true},
		{lang.Chinese, true},
		{lang.Unset, false},
		{lang.Code("jp"), false},
	}
	for _, tt := range tests {
		if got := tt.code.Translatable(); got != tt.want {
			t.Errorf("Code(%q).Translatable() = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestCodesAreAllTranslatable(t *testing.T) {
	codes := lang.Codes()
	if len(codes) != 3 {
		t.Fatalf("Codes() returned %d codes, want 3", len(codes))
	}
	for _, c := range codes {
		if !c.Translatable() {
			t.Errorf("Codes() included %q which is not translatable", c)
		}
	}
}
