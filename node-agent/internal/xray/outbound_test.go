package xray

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMarshalSOCKSOutboundFieldNumbers(t *testing.T) {
	handler := decodeFields(t, marshalSOCKSOutbound(SOCKSOutbound{
		Tag: "device-egress-x", Address: "127.0.0.1", Port: 10086, Username: "x", Password: "secret",
	}))
	if string(handler[1][0]) != "device-egress-x" {
		t.Fatalf("tag = %q", handler[1][0])
	}
	sender := decodeFields(t, handler[2][0])
	if string(sender[1][0]) != typeSenderConfig || len(sender[2][0]) != 0 {
		t.Fatalf("sender settings = %q", handler[2][0])
	}
	proxy := decodeFields(t, handler[3][0])
	if string(proxy[1][0]) != typeSOCKSClientConfig {
		t.Fatalf("proxy type = %q", proxy[1][0])
	}
	server := decodeFields(t, decodeFields(t, proxy[2][0])[1][0])
	address := decodeFields(t, server[1][0])
	if !bytes.Equal(address[1][0], []byte{127, 0, 0, 1}) {
		t.Fatalf("address = %x, want 4-byte IPv4", address[1][0])
	}
	if port, _ := binary.Uvarint(server[2][0]); port != 10086 {
		t.Fatalf("port = %d", port)
	}
	user := decodeFields(t, server[3][0])
	if _, ok := user[2]; ok {
		t.Fatal("socks user must not carry an email")
	}
	account := decodeFields(t, user[3][0])
	if string(account[1][0]) != typeSOCKSAccount {
		t.Fatalf("account type = %q", account[1][0])
	}
	credentials := decodeFields(t, account[2][0])
	if string(credentials[1][0]) != "x" || string(credentials[2][0]) != "secret" {
		t.Fatalf("credentials = %q", account[2][0])
	}
}

func TestMarshalIPOrDomainKeepsHostnamesAsDomains(t *testing.T) {
	if got := decodeFields(t, marshalIPOrDomain("egress.local")); string(got[2][0]) != "egress.local" {
		t.Fatalf("domain = %v", got)
	}
	if got := decodeFields(t, marshalIPOrDomain("::1")); len(got[1][0]) != 16 {
		t.Fatalf("ipv6 = %x", got[1][0])
	}
}

func TestMarshalReplaceRoutingRulesFieldNumbers(t *testing.T) {
	req := decodeFields(t, marshalAddRuleRequest(&addRuleRequest{Config: marshalTypedMessage(typeRouterConfig, marshalRouterConfig([]RoutingRule{
		{OutboundTag: "device-egress-x", UserEmail: []string{"x@proxima"}},
		{OutboundTag: "block", InboundTag: []string{"vless-in"}, RuleTag: "deny"},
	}))}))
	// shouldAppend must be absent (false): Xray then replaces the whole list.
	if _, ok := req[2]; ok {
		t.Fatal("rule swap sent shouldAppend=true; device rules would land after the deny fallback")
	}
	config := decodeFields(t, req[1][0])
	if string(config[1][0]) != typeRouterConfig {
		t.Fatalf("config type = %q", config[1][0])
	}
	rules := decodeFields(t, config[2][0])[2]
	if len(rules) != 2 {
		t.Fatalf("rules = %d", len(rules))
	}
	first, second := decodeFields(t, rules[0]), decodeFields(t, rules[1])
	if string(first[1][0]) != "device-egress-x" || string(first[7][0]) != "x@proxima" {
		t.Fatalf("device rule = %v", first)
	}
	if string(second[1][0]) != "block" || string(second[8][0]) != "vless-in" || string(second[19][0]) != "deny" {
		t.Fatalf("deny rule = %v", second)
	}
}

func TestMarshalRemoveOutboundFieldNumbers(t *testing.T) {
	if got := decodeFields(t, marshalRemoveOutboundRequest(&removeOutboundRequest{Tag: "device-egress-x"})); string(got[1][0]) != "device-egress-x" {
		t.Fatalf("remove = %v", got)
	}
}
