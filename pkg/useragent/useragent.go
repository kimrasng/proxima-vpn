// Package useragent provides a closed-set user-agent browser and OS classifier
// for admin audit trails. This package is for administrative visibility only
// and MUST NOT be used for fingerprinting, access control, or eligibility
// decisions.
//
// The classifier recognizes major browser and operating system families and
// categorizes unknown agents into a safe Unknown/UnknownOS bucket. Recognition
// follows strict precedence rules to handle user-agent overlaps safely: Edge
// before Chrome (both contain "Chrome"), Chrome before Safari, and IOS before
// macOS (both contain "Mac OS X").
package useragent

import (
	"strings"
)

// Browser represents a detected browser family.
type Browser string

const (
	Edge    Browser = "edge"
	Chrome  Browser = "chrome"
	Safari  Browser = "safari"
	Firefox Browser = "firefox"
	Unknown Browser = "unknown"
)

// Valid reports whether b is a known browser value.
func (b Browser) Valid() bool {
	switch b {
	case Edge, Chrome, Safari, Firefox, Unknown:
		return true
	}
	return false
}

// OS represents a detected operating system family.
type OS string

const (
	IOS       OS = "ios"
	MacOS     OS = "macos"
	Windows   OS = "windows"
	Linux     OS = "linux"
	Android   OS = "android"
	UnknownOS OS = "unknown"
)

// Valid reports whether o is a known OS value.
func (o OS) Valid() bool {
	switch o {
	case IOS, MacOS, Windows, Linux, Android, UnknownOS:
		return true
	}
	return false
}

// Classification is the result of classifying a user-agent string.
type Classification struct {
	Browser Browser
	OS      OS
}

// Classify parses a user-agent string and returns a closed-set Browser and OS
// classification. Unknown or malformed agents are classified as Unknown/UnknownOS.
//
// Precedence:
//   - Browser: Edge (before Chrome), Chrome (before Safari), Safari, Firefox
//   - OS: IOS (before macOS), macOS, Windows, Linux, Android
//
// Raw user-agent text is never included in the output.
func Classify(ua string) Classification {
	ua = strings.ToLower(ua)

	// Classify browser with safe precedence (Edge before Chrome, Chrome before Safari)
	browser := classifyBrowser(ua)

	// Classify OS with safe precedence (iOS before macOS)
	os := classifyOS(ua)

	return Classification{
		Browser: browser,
		OS:      os,
	}
}

func classifyBrowser(ua string) Browser {
	// Edge must be checked before Chrome because Edge UAs contain "Chrome"
	if strings.Contains(ua, "edg/") {
		return Edge
	}

	// Chrome must be checked before Safari because Chrome UAs contain "Safari"
	if strings.Contains(ua, "chrome/") {
		return Chrome
	}

	// Safari (general, covers both macOS and iOS Safari)
	if strings.Contains(ua, "safari/") {
		return Safari
	}

	// Firefox
	if strings.Contains(ua, "firefox/") {
		return Firefox
	}

	return Unknown
}

func classifyOS(ua string) OS {
	// IOS must be checked before macOS because iOS UAs contain "Mac OS X"
	if strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") {
		return IOS
	}

	// macOS
	if strings.Contains(ua, "macintosh") || strings.Contains(ua, "mac os x") {
		return MacOS
	}

	// Windows
	if strings.Contains(ua, "windows nt") {
		return Windows
	}

	// Android
	if strings.Contains(ua, "android") {
		return Android
	}

	// Linux (X11 is a common Linux indicator)
	if strings.Contains(ua, "x11") || strings.Contains(ua, "linux") {
		return Linux
	}

	return UnknownOS
}
