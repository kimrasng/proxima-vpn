package deviceegress_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestSessionAdmissionBoundToPassword(t *testing.T) {
	for _, command := range []byte{1, 3} {
		t.Run(map[byte]string{1: "TCP", 3: "UDP"}[command], func(t *testing.T) {
			s := startServer(t, allowAll, dialEcho(t))
			first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
			credentials := []devicebandwidth.Credential{
				{UUID: "device-a", Password: "first", SourceAddress: "10.0.0.2", SessionToken: first},
				{UUID: "device-a", Password: "second", SourceAddress: "10.0.0.2", SessionToken: second},
			}
			if err := s.SetCredentials(credentials); err != nil {
				t.Fatal(err)
			}
			calls := make(chan string, 2)
			s.SetAdmitter(func(context.Context, string) error {
				t.Error("session credential used legacy admission")
				return nil
			})
			s.SetSourceAdmitter(func(context.Context, string, string) error {
				t.Error("session credential used source admission")
				return nil
			})
			s.SetSessionAdmitter(func(ctx context.Context, uuid, token string) error {
				if uuid != "device-a" {
					t.Errorf("unexpected UUID %q", uuid)
				}
				calls <- token
				if token == second {
					return errors.New("session denied")
				}
				return nil
			})
			allowed := authenticate(t, s, "device-a", "first")
			if code, _ := request(t, allowed, command); code != 0 {
				t.Fatalf("allowed session failed: %d", code)
			}
			if token := <-calls; token != first {
				t.Fatalf("wrong session token %q", token)
			}
			denied := authenticate(t, s, "device-a", "second")
			if code, _ := request(t, denied, command); code != 2 {
				t.Fatalf("denied session received reply %d", code)
			}
			if token := <-calls; token != second {
				t.Fatalf("wrong session token %q", token)
			}
			mustClose(t, denied)
			if command == 1 {
				if _, err := allowed.Write([]byte("alive")); err != nil {
					t.Fatal(err)
				}
				var echo [5]byte
				if _, err := io.ReadFull(allowed, echo[:]); err != nil || string(echo[:]) != "alive" {
					t.Fatalf("other session did not survive: %q, %v", echo, err)
				}
			}
			credentials[0].SessionToken = strings.Repeat("c", 64)
			if err := s.SetCredentials(credentials); err != nil {
				t.Fatal(err)
			}
			mustClose(t, allowed)
		})
	}
}

func TestSessionAdmissionMissingCallbackFailsClosed(t *testing.T) {
	for _, command := range []byte{1, 3} {
		s := startServer(t, allowAll, dialEcho(t))
		if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a", SourceAddress: "10.0.0.2", SessionToken: strings.Repeat("a", 64)}}); err != nil {
			t.Fatal(err)
		}
		s.SetAdmitter(func(context.Context, string) error {
			t.Error("session credential fell back to legacy admission")
			return nil
		})
		s.SetSourceAdmitter(func(context.Context, string, string) error {
			t.Error("session credential fell back to source admission")
			return nil
		})
		c := authenticate(t, s, "device-a", "secret-a")
		if code, _ := request(t, c, command); code != 2 {
			t.Fatalf("missing session admitter received reply %d", code)
		}
		mustClose(t, c)
	}
}

func TestCredentialSessionTokenValidationIsAtomic(t *testing.T) {
	s := startServer(t, allowAll, nil)
	if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a"}}); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"a", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63) + " "} {
		if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "replacement", SessionToken: token}}); err == nil {
			t.Fatalf("invalid token accepted: %q", token)
		}
		_ = authenticate(t, s, "device-a", "secret-a").Close()
	}
	if err := s.SetCredentials([]devicebandwidth.Credential{
		{UUID: "device-a", Password: "secret-a", SessionToken: strings.Repeat("a", 64)},
		{UUID: "device-a", Password: "secret-a", SessionToken: strings.Repeat("b", 64)},
	}); err == nil {
		t.Fatal("duplicate password accepted")
	}
}
