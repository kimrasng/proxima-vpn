package deviceegress_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestSourceAdmissionBoundToCredential(t *testing.T) {
	for _, command := range []byte{1, 3} {
		t.Run(map[byte]string{1: "TCP", 3: "UDP"}[command], func(t *testing.T) {
			s := startServer(t, allowAll, dialEcho(t))
			credentials := []devicebandwidth.Credential{
				{UUID: "device-a", Password: "source-two", SourceAddress: "10.0.0.2"},
				{UUID: "device-a", Password: "source-three", SourceAddress: "10.0.0.3"},
				{UUID: "device-a", Password: "legacy"},
			}
			if err := s.SetCredentials(credentials); err != nil {
				t.Fatal(err)
			}
			calls := make(chan string, 3)
			legacyCalls := make(chan string, 1)
			s.SetAdmitter(func(ctx context.Context, uuid string) error {
				legacyCalls <- uuid
				return nil
			})
			s.SetSourceAdmitter(func(ctx context.Context, uuid, source string) error {
				if uuid != "device-a" {
					t.Errorf("unexpected UUID %q", uuid)
				}
				calls <- source
				if source == "10.0.0.3" {
					return errors.New("source denied")
				}
				return nil
			})
			allowed := authenticate(t, s, "device-a", "source-two")
			if code, _ := request(t, allowed, command); code != 0 {
				t.Fatalf("allowed source failed: %d", code)
			}
			if source := <-calls; source != "10.0.0.2" {
				t.Fatalf("wrong bound source: %q", source)
			}
			denied := authenticate(t, s, "device-a", "source-three")
			if code, _ := request(t, denied, command); code != 2 {
				t.Fatalf("denied source received reply %d", code)
			}
			if source := <-calls; source != "10.0.0.3" {
				t.Fatalf("wrong bound source: %q", source)
			}
			mustClose(t, denied)
			if command == 1 {
				if _, err := allowed.Write([]byte("alive")); err != nil {
					t.Fatal(err)
				}
				var echo [5]byte
				if _, err := io.ReadFull(allowed, echo[:]); err != nil || string(echo[:]) != "alive" {
					t.Fatalf("other source did not survive: %q, %v", echo, err)
				}
			}
			legacy := authenticate(t, s, "device-a", "legacy")
			if code, _ := request(t, legacy, command); code != 0 {
				t.Fatalf("legacy admission failed: %d", code)
			}
			if uuid := <-legacyCalls; uuid != "device-a" {
				t.Fatalf("wrong legacy UUID %q", uuid)
			}
			select {
			case source := <-calls:
				t.Fatalf("legacy used source admission: %q", source)
			default:
			}
			// Rebinding a password revokes the session using its previous source.
			credentials[0].SourceAddress = "10.0.0.4"
			if err := s.SetCredentials(credentials); err != nil {
				t.Fatal(err)
			}
			mustClose(t, allowed)
		})
	}
}

func TestSourceAdmissionMissingCallbackFailsClosed(t *testing.T) {
	for _, command := range []byte{1, 3} {
		s := startServer(t, allowAll, dialEcho(t))
		if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a", SourceAddress: "10.0.0.2"}}); err != nil {
			t.Fatal(err)
		}
		s.SetAdmitter(func(context.Context, string) error {
			t.Error("source credential must not fall back to legacy admission")
			return nil
		})
		c := authenticate(t, s, "device-a", "secret-a")
		if code, _ := request(t, c, command); code != 2 {
			t.Fatalf("missing source admitter received reply %d", code)
		}
		mustClose(t, c)
	}
}

func TestCredentialSourceValidationIsAtomic(t *testing.T) {
	s := startServer(t, allowAll, nil)
	for _, source := range []string{" ", "10.0.0.2 ", "host.example", "10.0.0.2/32", "10.0.0.2:80", "0.0.0.0", "255.255.255.255", "224.0.0.1", "::", "ff02::1", "fe80::2%eth0", "::ffff:10.0.0.2", "2001:0db8::2"} {
		t.Run(source, func(t *testing.T) {
			if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "replacement", SourceAddress: source}}); err == nil {
				t.Fatalf("invalid source accepted: %q", source)
			}
			_ = authenticate(t, s, "device-a", "secret-a").Close()
		})
	}
	for _, source := range []string{"", "10.0.0.2", "127.0.0.2", "127.0.0.3", "::1", "169.254.1.2", "fe80::2", "2001:db8::2"} {
		if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a", SourceAddress: source}}); err != nil {
			t.Fatalf("valid source %q rejected: %v", source, err)
		}
	}
	for _, source := range []string{"2001:db8::2", "10.0.0.3", ""} {
		if err := s.SetCredentials([]devicebandwidth.Credential{
			{UUID: "device-a", Password: "secret-a", SourceAddress: "2001:db8::2"},
			{UUID: "device-a", Password: "secret-a", SourceAddress: source},
		}); err == nil {
			t.Fatal("duplicate credential accepted")
		}
	}
}
