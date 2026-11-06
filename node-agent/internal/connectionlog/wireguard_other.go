//go:build !linux

package connectionlog

func openWGCapture(int) (wgCapture, error) { return nil, ErrWireGuardUnsupported }

func wgCaptureSupported() bool { return false }

func wgKernelRelease() (string, error) { return "", nil }
