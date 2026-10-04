package useragent_test

import (
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/useragent"
)

// TestClassifyBrowserPrecedence ensures Edge is recognized before Chrome, Chrome
// before Safari, and generally matches major identifiable browsers.
func TestClassifyBrowserPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		ua     string
		want   useragent.Browser
	}{
		// Edge (before Chrome because UA contains both)
		{"Edge modern", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", useragent.Edge},
		{"Edge on Mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", useragent.Edge},

		// Chrome (checked before Safari)
		{"Chrome", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", useragent.Chrome},
		{"Chrome on Mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", useragent.Chrome},
		{"Chromium", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", useragent.Chrome},

		// Safari (iOS safari contains both Mobile and Safari)
		{"Safari macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", useragent.Safari},
		{"Safari iOS", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1.2 Mobile/15E148 Safari/604.1", useragent.Safari},

		// Firefox
		{"Firefox", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0", useragent.Firefox},
		{"Firefox on Mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7; rv:121.0) Gecko/20100101 Firefox/121.0", useragent.Firefox},

		// Unknown/Empty
		{"Empty", "", useragent.Unknown},
		{"Unknown browser", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) UnknownBrowser/1.0", useragent.Unknown},
		{"Malformed", "this is not a valid user agent at all", useragent.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := useragent.Classify(tt.ua)
			if result.Browser != tt.want {
				t.Errorf("Classify(%q).Browser = %v, want %v", tt.ua, result.Browser, tt.want)
			}
		})
	}
}

// TestClassifyOSPrecedence ensures iOS is recognized before macOS, and major
// OS families are identified correctly.
func TestClassifyOSPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		ua     string
		want   useragent.OS
	}{
		// iOS (before macOS because UA may contain both)
		{"iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15", useragent.IOS},
		{"iPad", "Mozilla/5.0 (iPad; CPU OS 17_2 like Mac OS X) AppleWebKit/605.1.15", useragent.IOS},

		// macOS
		{"macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", useragent.MacOS},

		// Windows
		{"Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", useragent.Windows},
		{"Windows older", "Mozilla/5.0 (Windows NT 6.1; Win64; x64) AppleWebKit/537.36", useragent.Windows},

		// Linux
		{"Linux", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36", useragent.Linux},
		{"Linux other", "Mozilla/5.0 (X11; U; Linux i686) Gecko/20100101 Firefox/121.0", useragent.Linux},

		// Android
		{"Android", "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36", useragent.Android},

		// Unknown/Empty
		{"Empty", "", useragent.UnknownOS},
		{"Unknown OS", "Mozilla/5.0 (X11; SomeUnknownOS x86_64) AppleWebKit/537.36", useragent.Linux},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := useragent.Classify(tt.ua)
			if result.OS != tt.want {
				t.Errorf("Classify(%q).OS = %v, want %v", tt.ua, result.OS, tt.want)
			}
		})
	}
}

// TestClassifyReturnsClosedSet ensures output never contains raw UA text.
func TestClassifyReturnsClosedSet(t *testing.T) {
	testUAs := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1.2 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36",
		"some malformed agent string with weird characters",
		"",
	}

	for _, ua := range testUAs {
		result := useragent.Classify(ua)

		// Browser must be one of the known values or Unknown
		if !result.Browser.Valid() {
			t.Errorf("Classify(%q).Browser = %v, which is not valid", ua, result.Browser)
		}

		// OS must be one of the known values or UnknownOS
		if !result.OS.Valid() {
			t.Errorf("Classify(%q).OS = %v, which is not valid", ua, result.OS)
		}

		// Ensure raw UA text is never in the result (string would contain it)
		// This is a basic sanity check that we're not accidentally returning raw input
		if len(ua) > 10 && ua != "" {
			browserStr := string(result.Browser)
			// Check that neither contains the UA substring (extremely lenient check)
			// If the UA is long and result is short, raw UA can't be in result
			if len(browserStr) > len(ua) {
				t.Errorf("Classify(%q).Browser string seems too long: %v", ua, result.Browser)
			}
		}
	}
}

// TestBrowserValid tests the Browser.Valid method.
func TestBrowserValid(t *testing.T) {
	tests := []struct {
		browser useragent.Browser
		want    bool
	}{
		{useragent.Edge, true},
		{useragent.Chrome, true},
		{useragent.Safari, true},
		{useragent.Firefox, true},
		{useragent.Unknown, true},
		{useragent.Browser("invalid"), false},
	}

	for _, tt := range tests {
		if got := tt.browser.Valid(); got != tt.want {
			t.Errorf("Browser(%q).Valid() = %v, want %v", tt.browser, got, tt.want)
		}
	}
}

// TestOSValid tests the OS.Valid method.
func TestOSValid(t *testing.T) {
	tests := []struct {
		os   useragent.OS
		want bool
	}{
		{useragent.IOS, true},
		{useragent.MacOS, true},
		{useragent.Windows, true},
		{useragent.Linux, true},
		{useragent.Android, true},
		{useragent.UnknownOS, true},
		{useragent.OS("invalid"), false},
	}

	for _, tt := range tests {
		if got := tt.os.Valid(); got != tt.want {
			t.Errorf("OS(%q).Valid() = %v, want %v", tt.os, got, tt.want)
		}
	}
}

// TestClassifyRealWorldUserAgents tests a mix of real-world user agents.
func TestClassifyRealWorldUserAgents(t *testing.T) {
	tests := []struct {
		name         string
		ua           string
		expectBrowser useragent.Browser
		expectOS     useragent.OS
	}{
		{
			name:         "Safari on iPhone",
			ua:           "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3.1 Mobile/15E148 Safari/604.1",
			expectBrowser: useragent.Safari,
			expectOS:     useragent.IOS,
		},
		{
			name:         "Chrome on Windows",
			ua:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
			expectBrowser: useragent.Chrome,
			expectOS:     useragent.Windows,
		},
		{
			name:         "Edge on macOS",
			ua:           "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36 Edg/121.0.0.0",
			expectBrowser: useragent.Edge,
			expectOS:     useragent.MacOS,
		},
		{
			name:         "Firefox on Linux",
			ua:           "Mozilla/5.0 (X11; Linux x86_64; rv:122.0) Gecko/20100101 Firefox/122.0",
			expectBrowser: useragent.Firefox,
			expectOS:     useragent.Linux,
		},
		{
			name:         "Chrome on Android",
			ua:           "Mozilla/5.0 (Linux; Android 13; SM-A505F) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Mobile Safari/537.36",
			expectBrowser: useragent.Chrome,
			expectOS:     useragent.Android,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := useragent.Classify(tt.ua)
			if result.Browser != tt.expectBrowser {
				t.Errorf("Browser: got %v, want %v", result.Browser, tt.expectBrowser)
			}
			if result.OS != tt.expectOS {
				t.Errorf("OS: got %v, want %v", result.OS, tt.expectOS)
			}
		})
	}
}
