package xray

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
)

// Hand-encoded protobuf for adding and removing per-device egress outbounds
// and their routing rules on a running Xray, so admitting a device never
// restarts Xray and never drops other devices' sessions.
//
// Field numbers are from Xray-core's .proto definitions:
//
//	app/proxyman/command/command.proto
//	  AddOutboundRequest    { core.OutboundHandlerConfig outbound = 1; }
//	  RemoveOutboundRequest { string tag = 1; }
//	core/config.proto
//	  OutboundHandlerConfig { string tag = 1; TypedMessage sender_settings = 2;
//	                          TypedMessage proxy_settings = 3; ... }
//	proxy/socks/config.proto
//	  ClientConfig { xray.common.protocol.ServerEndpoint server = 1; }
//	  Account      { string username = 1; string password = 2; }
//	common/protocol/server_spec.proto
//	  ServerEndpoint { IPOrDomain address = 1; uint32 port = 2; User user = 3; }
//	common/net/address.proto
//	  IPOrDomain { oneof address { bytes ip = 1; string domain = 2; } }
//	app/router/command/command.proto
//	  AddRuleRequest    { TypedMessage config = 1; bool shouldAppend = 2; }
//	  RemoveRuleRequest { string ruleTag = 1; }
//	app/router/config.proto
//	  Config      { DomainStrategy domain_strategy = 1; repeated RoutingRule rule = 2; ... }
//	  RoutingRule { string tag = 1 (oneof target_tag); repeated string user_email = 7;
//	                repeated string inbound_tag = 8; string rule_tag = 19; ... }
const (
	addOutboundMethod    = handlerServicePath + "/AddOutbound"
	removeOutboundMethod = handlerServicePath + "/RemoveOutbound"

	routingServicePath = "/xray.app.router.command.RoutingService"
	addRuleMethod      = routingServicePath + "/AddRule"

	typeSOCKSClientConfig = "xray.proxy.socks.ClientConfig"
	typeSOCKSAccount      = "xray.proxy.socks.Account"
	typeSenderConfig      = "xray.app.proxyman.SenderConfig"
	typeRouterConfig      = "xray.app.router.Config"
)

type addOutboundRequest struct{ Outbound []byte }
type removeOutboundRequest struct{ Tag string }
type addRuleRequest struct {
	Config       []byte
	ShouldAppend bool
}
type emptyResponse struct{}

func marshalAddOutboundRequest(r *addOutboundRequest) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	return appendBytes(buf, r.Outbound)
}

func marshalRemoveOutboundRequest(r *removeOutboundRequest) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	return appendBytes(buf, []byte(r.Tag))
}

func marshalAddRuleRequest(r *addRuleRequest) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	buf = appendBytes(buf, r.Config)
	if r.ShouldAppend {
		buf = appendTag(buf, 2, 0)
		buf = binary.AppendUvarint(buf, 1)
	}
	return buf
}

// marshalIPOrDomain encodes an address the way Xray's NewIPOrDomain does: IP
// literals as 4/16 raw bytes, anything else as a domain.
func marshalIPOrDomain(address string) []byte {
	var buf []byte
	if ip := net.ParseIP(address); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		buf = appendTag(buf, 1, 2)
		return appendBytes(buf, ip)
	}
	buf = appendTag(buf, 2, 2)
	return appendBytes(buf, []byte(address))
}

// SOCKSOutbound is a SOCKS5 client outbound with one authenticated user, the
// shape of a per-device egress outbound.
type SOCKSOutbound struct {
	Tag      string
	Address  string
	Port     uint32
	Username string
	Password string
}

// marshalSOCKSOutbound encodes core.OutboundHandlerConfig for o. An empty
// SenderConfig matches what Xray's JSON loader builds for an outbound without
// streamSettings, proxySettings or mux.
func marshalSOCKSOutbound(o SOCKSOutbound) []byte {
	var account []byte
	account = appendTag(account, 1, 2)
	account = appendBytes(account, []byte(o.Username))
	account = appendTag(account, 2, 2)
	account = appendBytes(account, []byte(o.Password))

	user := marshalUser(0, "", marshalTypedMessage(typeSOCKSAccount, account))

	var server []byte
	server = appendTag(server, 1, 2)
	server = appendBytes(server, marshalIPOrDomain(o.Address))
	server = appendTag(server, 2, 0)
	server = binary.AppendUvarint(server, uint64(o.Port))
	server = appendTag(server, 3, 2)
	server = appendBytes(server, user)

	var client []byte
	client = appendTag(client, 1, 2)
	client = appendBytes(client, server)

	var handler []byte
	handler = appendTag(handler, 1, 2)
	handler = appendBytes(handler, []byte(o.Tag))
	handler = appendTag(handler, 2, 2)
	handler = appendBytes(handler, marshalTypedMessage(typeSenderConfig, nil))
	handler = appendTag(handler, 3, 2)
	handler = appendBytes(handler, marshalTypedMessage(typeSOCKSClientConfig, client))
	return handler
}

// RoutingRule is the subset of a field rule the agent installs at runtime.
type RoutingRule struct {
	RuleTag      string
	OutboundTag  string
	UserEmail    []string
	InboundTag   []string
	SourceCIDRs  []string
	SourcePorts  []uint16
	SessionToken string
}

func marshalRoutingRule(r RoutingRule) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	buf = appendBytes(buf, []byte(r.OutboundTag))
	for _, email := range r.UserEmail {
		buf = appendTag(buf, 7, 2)
		buf = appendBytes(buf, []byte(email))
	}
	for _, tag := range r.InboundTag {
		buf = appendTag(buf, 8, 2)
		buf = appendBytes(buf, []byte(tag))
	}
	for _, source := range r.SourceCIDRs {
		ip, network, err := net.ParseCIDR(source)
		if err != nil {
			continue
		} // ReplaceRoutingRules validates before encoding.
		bits, _ := network.Mask.Size()
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		} else {
			ip = ip.To16()
		}
		var cidr []byte
		cidr = appendTag(cidr, 1, 2)
		cidr = appendBytes(cidr, ip)
		cidr = appendTag(cidr, 2, 0)
		cidr = binary.AppendUvarint(cidr, uint64(bits))
		var custom []byte
		custom = appendTag(custom, 1, 2)
		custom = appendBytes(custom, cidr)
		var rule []byte
		rule = appendTag(rule, 2, 2)
		rule = appendBytes(rule, custom)
		buf = appendTag(buf, 11, 2)
		buf = appendBytes(buf, rule)
	}
	if len(r.SourcePorts) > 0 {
		if r.SessionToken != "" {
			decoded, err := hex.DecodeString(r.SessionToken)
			if len(r.SessionToken) != 64 || err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != r.SessionToken || len(r.SourceCIDRs) != 1 {
				return buf
			}
			buf = appendTag(buf, 14, 2)
			buf = appendBytes(buf, []byte(r.SessionToken))
		}
		var ports []byte
		for _, port := range r.SourcePorts {
			var span []byte
			span = appendTag(span, 1, 0)
			span = binary.AppendUvarint(span, uint64(port))
			span = appendTag(span, 2, 0)
			span = binary.AppendUvarint(span, uint64(port))
			ports = appendTag(ports, 1, 2)
			ports = appendBytes(ports, span)
		}
		buf = appendTag(buf, 16, 2)
		buf = appendBytes(buf, ports)
	}
	if r.RuleTag != "" || r.SessionToken != "" {
		if r.RuleTag == "" {
			r.RuleTag = "session-" + r.SessionToken
		}
		buf = appendTag(buf, 19, 2)
		buf = appendBytes(buf, []byte(r.RuleTag))
	}
	return buf
}

// marshalRouterConfig encodes app/router Config{rule...}. Xray's ReloadRules
// replaces only rules and balancers, leaving the domain strategy as loaded.
func marshalRouterConfig(rules []RoutingRule) []byte {
	var buf []byte
	for _, rule := range rules {
		buf = appendTag(buf, 2, 2)
		buf = appendBytes(buf, marshalRoutingRule(rule))
	}
	return buf
}

// AddSOCKSOutbound registers a SOCKS outbound on the running Xray.
func (c *StatsClient) AddSOCKSOutbound(ctx context.Context, o SOCKSOutbound) error {
	if o.Tag == "" || o.Address == "" || o.Port == 0 {
		return fmt.Errorf("add socks outbound: tag, address and port are required")
	}
	req := &addOutboundRequest{Outbound: marshalSOCKSOutbound(o)}
	if err := c.conn.Invoke(ctx, addOutboundMethod, req, &emptyResponse{}); err != nil {
		return fmt.Errorf("add outbound %s: %w", o.Tag, err)
	}
	return nil
}

// RemoveOutbound removes an outbound from the running Xray by tag.
func (c *StatsClient) RemoveOutbound(ctx context.Context, tag string) error {
	if tag == "" {
		return fmt.Errorf("remove outbound: tag is required")
	}
	if err := c.conn.Invoke(ctx, removeOutboundMethod, &removeOutboundRequest{Tag: tag}, &emptyResponse{}); err != nil {
		return fmt.Errorf("remove outbound %s: %w", tag, err)
	}
	return nil
}

// ReplaceRoutingRules atomically swaps the running router's whole rule list
// for rules, in order. Xray's AddRule only appends after existing rules, which
// would land a new device's rule behind the deny fallback, so the agent always
// supplies the complete ordered list instead.
func (c *StatsClient) ReplaceRoutingRules(ctx context.Context, rules []RoutingRule) error {
	for _, rule := range rules {
		if rule.SessionToken != "" {
			token := rule.SessionToken
			decoded, err := hex.DecodeString(token)
			if len(token) != 64 || err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != token || len(rule.SourceCIDRs) != 1 || len(rule.SourcePorts) != 1 || rule.RuleTag != "" {
				return fmt.Errorf("invalid session-qualified routing rule")
			}
		}
		if len(rule.SourcePorts) > 0 && len(rule.SourceCIDRs) != 1 {
			return fmt.Errorf("source-port rule requires one source network")
		}
		for _, port := range rule.SourcePorts {
			if port == 0 {
				return fmt.Errorf("source-port rule has zero port")
			}
		}
		for _, source := range rule.SourceCIDRs {
			ip, network, err := net.ParseCIDR(source)
			if err != nil {
				return fmt.Errorf("invalid source CIDR %q", source)
			}
			if len(rule.SourcePorts) > 0 {
				bits, total := network.Mask.Size()
				if bits != total || ip.IsUnspecified() || ip.IsMulticast() {
					return fmt.Errorf("source-port rule requires exact unicast host")
				}
			}
		}
	}
	req := &addRuleRequest{Config: marshalTypedMessage(typeRouterConfig, marshalRouterConfig(rules))}
	if err := c.conn.Invoke(ctx, addRuleMethod, req, &emptyResponse{}); err != nil {
		return fmt.Errorf("replace routing rules: %w", err)
	}
	return nil
}
