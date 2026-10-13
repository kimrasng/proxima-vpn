package deviceegress_test

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

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
			entered := make(chan string, 2)
			left := make(chan string, 2)
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
			s.SetSessionEnterer(func(ctx context.Context, uuid, token string) error {
				entered <- token
				return nil
			})
			s.SetSessionLeaver(func(ctx context.Context, uuid, token string) error {
				left <- token
				return nil
			})
			allowed := authenticate(t, s, "device-a", "first")
			if code, _ := request(t, allowed, command); code != 0 {
				t.Fatalf("allowed session failed: %d", code)
			}
			if token := <-calls; token != first {
				t.Fatalf("wrong session token %q", token)
			}
			if token := <-entered; token != first {
				t.Fatalf("wrong entered session token %q", token)
			}
			select {
			case token := <-left:
				t.Fatalf("session slot released before tunnel close: %q", token)
			default:
			}
			denied := authenticate(t, s, "device-a", "second")
			if code, _ := request(t, denied, command); code != 2 {
				t.Fatalf("denied session received reply %d", code)
			}
			if token := <-calls; token != second {
				t.Fatalf("wrong session token %q", token)
			}
			select {
			case token := <-entered:
				t.Fatalf("denied session acquired a slot: %q", token)
			default:
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
			select {
			case token := <-left:
				if token != first {
					t.Fatalf("wrong released session token %q", token)
				}
			case <-time.After(time.Second):
				t.Fatal("session slot was not released after tunnel close")
			}
		})
	}
}

func TestSessionSlotIsAcquiredPerAuthenticatedTunnel(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	token := strings.Repeat("a", 64)
	if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a", SessionToken: token}}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan string, 2)
	left := make(chan string, 2)
	s.SetSessionAdmitter(func(context.Context, string, string) error { return nil })
	s.SetSessionEnterer(func(_ context.Context, uuid, gotToken string) error {
		if uuid != "device-a" || gotToken != token {
			t.Errorf("unexpected enter identity: %q %q", uuid, gotToken)
		}
		entered <- gotToken
		return nil
	})
	s.SetSessionLeaver(func(_ context.Context, uuid, gotToken string) error {
		if uuid != "device-a" || gotToken != token {
			t.Errorf("unexpected leave identity: %q %q", uuid, gotToken)
		}
		left <- gotToken
		return nil
	})

	connections := []net.Conn{authenticate(t, s, "device-a", "secret-a"), authenticate(t, s, "device-a", "secret-a")}
	for _, connection := range connections {
		if code, _ := request(t, connection, 3); code != 0 {
			t.Fatalf("UDP ASSOCIATE failed: %d", code)
		}
	}
	for range connections {
		if got := <-entered; got != token {
			t.Fatalf("unexpected entered token %q", got)
		}
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for range connections {
		select {
		case got := <-left:
			if got != token {
				t.Fatalf("unexpected released token %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("session tunnel did not release its slot")
		}
	}
}

func TestSessionSlotReleaseFailureDoesNotFreeRemoteReservation(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	token := strings.Repeat("a", 64)
	if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a", SessionToken: token}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	reserved := false
	left := make(chan struct{}, 1)
	s.SetSessionAdmitter(func(context.Context, string, string) error { return nil })
	s.SetSessionEnterer(func(context.Context, string, string) error {
		mu.Lock()
		defer mu.Unlock()
		if reserved {
			return errors.New("slot remains reserved")
		}
		reserved = true
		return nil
	})
	s.SetSessionLeaver(func(context.Context, string, string) error {
		left <- struct{}{}
		return errors.New("release not confirmed")
	})
	first := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, first, 3); code != 0 {
		t.Fatalf("initial UDP ASSOCIATE failed: %d", code)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-left:
	case <-time.After(time.Second):
		t.Fatal("session leaver was not called")
	}
	second := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, second, 3); code == 0 {
		t.Fatal("session received a slot after release failed")
	}
}

func TestSessionSlotEnterFailureDeniesBeforeSuccess(t *testing.T) {
	for _, command := range []byte{1, 3} {
		t.Run(map[byte]string{1: "TCP", 3: "UDP"}[command], func(t *testing.T) {
			s := startServer(t, allowAll, dialEcho(t))
			if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a", SessionToken: strings.Repeat("a", 64)}}); err != nil {
				t.Fatal(err)
			}
			s.SetSessionAdmitter(func(context.Context, string, string) error { return nil })
			var entered, left int
			s.SetSessionEnterer(func(context.Context, string, string) error {
				entered++
				return errors.New("session slot full")
			})
			s.SetSessionLeaver(func(context.Context, string, string) error {
				left++
				return nil
			})
			c := authenticate(t, s, "device-a", "secret-a")
			if code, _ := request(t, c, command); code == 0 {
				t.Fatal("session received success after slot acquisition failed")
			}
			if entered != 1 || left != 0 {
				t.Fatalf("unexpected slot callbacks: entered=%d left=%d", entered, left)
			}
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
