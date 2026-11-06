package nodeprov

import (
	"fmt"
	"strings"
)

func historyInvalid(field string) error {
	return fmt.Errorf("%w: %s", ErrHistoryInvalid, field)
}
func historyOne(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func historyASCII(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
func historyEntityID(id *string, kind string) bool {
	if id == nil {
		return false
	}
	p := strings.Split(*id, ":")
	if len(p) != 3 || HistoryUUID(p[0]).Validate() != nil || p[1] != kind {
		return false
	}
	var n HistoryU64
	return n.UnmarshalJSON([]byte(`"`+p[2]+`"`)) == nil && n > 0
}

func (e HistoryEvent) Validate() error {
	if e.SchemaVersion != 1 || e.Sequence == 0 || e.ObserverBootID.Validate() != nil ||
		e.CollectionEpoch.Validate() != nil || e.EventID != string(e.ObserverBootID)+":"+e.Sequence.String() {
		return historyInvalid("event identity")
	}
	if !historyASCII(e.ProducerID) || !historyASCII(e.RuntimeBuild) ||
		!historyOne(e.Runtime, "xray", "hysteria2", "wireguard", "agent") || e.EventTime.Validate() != nil {
		return historyInvalid("producer or time")
	}
	if err := e.Auth.Validate(); err != nil {
		return err
	}
	if err := e.Quality.Validate(); err != nil {
		return err
	}
	if e.Auth.Kind != "device" && e.Quality.Identity != "unknown" {
		return historyInvalid("unknown account attribution")
	}
	if e.Peer != nil && (!historyIP(e.Peer.IP) || !historyPort(e.Peer.Port)) {
		return historyInvalid("peer")
	}
	if e.InboundTag != nil && !historyText(*e.InboundTag, 128) {
		return historyInvalid("inbound")
	}
	for _, t := range []*HistoryTime{e.Lifecycle.StartedAt, e.Lifecycle.LastObservedAt, e.Lifecycle.EndedAt} {
		if t != nil && t.Validate() != nil {
			return historyInvalid("lifecycle timestamp")
		}
	}
	switch e.Kind {
	case "collection_boundary", "collection_gap":
		return e.validateCollection()
	case "tunnel_start", "tunnel_update", "tunnel_end", "flow_start", "flow_update", "flow_end":
		return e.validateEntity()
	default:
		return historyInvalid("kind")
	}
}

func (e HistoryEvent) validateCollection() error {
	l := e.Lifecycle
	if e.Protocol != nil || e.InboundTag != nil || e.Peer != nil || e.Destination != nil ||
		e.TunnelID != nil || e.FlowID != nil || e.Auth.Kind != "unknown" ||
		l.Scope != "collection" || l.StartedAt != nil || l.LastObservedAt != nil ||
		l.EndedAt != nil || l.EndReason != nil || l.EndSemantics != nil || l.PartialStart ||
		e.Bytes != (HistoryBytes{Layer: "none"}) {
		return historyInvalid("collection fields")
	}
	q := e.Quality
	if q.Identity != "unknown" || q.Parent != "unknown" || q.Destination != "unknown" ||
		q.Bytes != "unknown" || q.Start != "unknown" || q.End != "unknown" {
		return historyInvalid("collection quality")
	}
	switch e.Kind {
	case "collection_boundary":
		if e.Boundary == nil || e.Gap != nil ||
			!historyOne(e.Boundary.Action, "enabled", "disabled", "reconfigured") ||
			e.Boundary.PriorRevision > e.ConfigRevision {
			return historyInvalid("boundary")
		}
	case "collection_gap":
		if e.Gap == nil || e.Boundary != nil || q.Completeness != "unknown" {
			return historyInvalid("gap")
		}
		return e.Gap.Validate()
	}
	return nil
}

func (e HistoryEvent) validateEntity() error {
	if e.Gap != nil || e.Boundary != nil || e.Protocol == nil ||
		!historyOne(*e.Protocol, "vless", "vmess", "trojan", "shadowsocks", "shadowsocks2022", "hysteria2", "wireguard") {
		return historyInvalid("entity protocol")
	}
	switch e.Runtime {
	case "xray":
		if !historyOne(*e.Protocol, "vless", "vmess", "trojan", "shadowsocks", "shadowsocks2022") {
			return historyInvalid("runtime protocol")
		}
	case "hysteria2", "wireguard":
		if *e.Protocol != e.Runtime {
			return historyInvalid("runtime protocol")
		}
	}
	l, q := e.Lifecycle, e.Quality
	if l.LastObservedAt == nil || (l.StartedAt == nil && q.Start != "unknown") ||
		(l.PartialStart && l.StartedAt != nil) || (e.TunnelID == nil && q.Parent != "unknown") {
		return historyInvalid("entity observation quality")
	}
	if strings.HasPrefix(e.Kind, "tunnel_") {
		if !historyEntityID(e.TunnelID, "t") || e.FlowID != nil || e.Destination != nil ||
			!historyOne(l.Scope, "authenticated_transport", "activity_window") ||
			e.Bytes != (HistoryBytes{Layer: "none"}) || q.Bytes != "unknown" || q.Destination != "unknown" {
			return historyInvalid("tunnel fields")
		}
	} else {
		if !historyEntityID(e.FlowID, "f") || (e.TunnelID != nil && !historyEntityID(e.TunnelID, "t")) ||
			!historyOne(l.Scope, "proxy_flow", "activity_window") || e.Destination == nil {
			return historyInvalid("flow fields")
		}
		if err := e.Destination.Validate(); err != nil {
			return err
		}
		if !historyOne(e.Bytes.Layer, "proxy_payload_observed", "inner_ip_observed") {
			return historyInvalid("flow byte layer")
		}
		if (*e.Protocol == "wireguard") != (e.Bytes.Layer == "inner_ip_observed") {
			return historyInvalid("protocol byte layer")
		}
		if e.Destination.RequestedHost == nil && e.Destination.EffectiveHost == nil &&
			e.Destination.ObservedIP == nil && q.Destination != "unknown" {
			return historyInvalid("unknown destination quality")
		}
		if (e.Bytes.Upload == nil || e.Bytes.Download == nil) && (q.Bytes != "unknown" || q.Completeness != "unknown" || e.Bytes.Final) {
			return historyInvalid("unknown counters")
		}
		if e.Kind == "flow_start" && (e.Bytes.Upload == nil || e.Bytes.Download == nil || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0) {
			return historyInvalid("zero start baseline")
		}
		if *e.Protocol == "wireguard" && (e.Bytes.Layer != "inner_ip_observed" ||
			e.Destination.RequestedHost != nil || e.Destination.RequestedPort != nil || l.Scope != "activity_window") {
			return historyInvalid("wireguard observation")
		}
	}
	if strings.HasSuffix(e.Kind, "_end") {
		if l.EndedAt == nil || l.EndReason == nil || l.EndSemantics == nil ||
			!historyOne(*l.EndReason, "eof", "transport_close", "io_error", "policy_reject", "dial_error", "idle_timeout", "peer_removed", "interface_removed", "parent_changed", "tuple_reused", "rst", "fin") ||
			!historyOne(*l.EndSemantics, "local_owner_closed", "activity_expired", "observation_segment_closed") {
			return historyInvalid("end fields")
		}
		if l.Scope == "activity_window" && *l.EndSemantics == "local_owner_closed" {
			return historyInvalid("activity end semantics")
		}
	} else if l.EndedAt != nil || l.EndReason != nil || l.EndSemantics != nil || q.End != "unknown" || e.Bytes.Final {
		return historyInvalid("nonterminal end")
	}
	if l.Scope == "activity_window" && (q.Start == "exact" || q.End == "exact") {
		return historyInvalid("activity endpoint quality")
	}
	if e.Destination == nil && q.Destination != "unknown" {
		return historyInvalid("destination quality")
	}
	return nil
}
