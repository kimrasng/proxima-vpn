package nodeprov

import (
	"net/netip"
	"unicode/utf8"
)

func historyText(s string, max int) bool { return len(s) <= max && utf8.ValidString(s) }
func historyPort(p int) bool             { return p >= 1 && p <= 65535 }
func historyIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Zone() == "" && a.Unmap().String() == s
}

func (a HistoryAuth) Validate() error {
	switch a.Kind {
	case "unknown":
		if a.CredentialRef != nil || a.BindingRevision != nil {
			return historyInvalid("unknown auth")
		}
	case "device", "shared":
		if a.CredentialRef == nil || a.BindingRevision == nil || a.CredentialRef.Validate() != nil {
			return historyInvalid("bound auth")
		}
	default:
		return historyInvalid("auth kind")
	}
	return nil
}

func (d HistoryDestination) Validate() error {
	if !historyOne(d.Transport, "tcp", "udp") {
		return historyInvalid("transport")
	}
	for _, h := range []*string{d.RequestedHost, d.EffectiveHost} {
		if h == nil {
			continue
		}
		if !historyText(*h, 1024) || *h == "" {
			return historyInvalid("host")
		}
		if a, err := netip.ParseAddr(*h); err == nil && (a.Zone() != "" || a.Unmap().String() != *h) {
			return historyInvalid("canonical host IP")
		}
	}
	for _, p := range []*int{d.RequestedPort, d.ObservedPort} {
		if p != nil && !historyPort(*p) {
			return historyInvalid("destination port")
		}
	}
	if d.ObservedIP != nil && !historyIP(*d.ObservedIP) {
		return historyInvalid("observed IP")
	}
	return nil
}

func (q HistoryQuality) Validate() error {
	for _, s := range []string{q.Identity, q.Parent, q.Destination, q.Bytes, q.Start, q.End} {
		if !historyOne(s, "exact", "observed", "unknown") {
			return historyInvalid("quality")
		}
	}
	if !historyOne(q.Completeness, "complete", "partial", "unknown") {
		return historyInvalid("completeness")
	}
	return nil
}

func (g HistoryGap) Validate() error {
	if !historyASCII(g.ProducerID) || g.ObserverBootID.Validate() != nil ||
		!historyOne(g.Reason, "producer_lost", "observer_restart", "export_overflow", "outbox_full", "disk_error", "capture_drop", "decode_error", "identity_map_changed", "unsupported_packet", "counter_overflow") {
		return historyInvalid("gap source/reason")
	}
	if (g.FirstSequence == nil) != (g.LastSequence == nil) {
		return historyInvalid("gap range pair")
	}
	if g.FirstSequence != nil && (*g.FirstSequence == 0 || *g.LastSequence < *g.FirstSequence) {
		return historyInvalid("gap range")
	}
	for _, t := range []*HistoryTime{g.FromTime, g.ToTime} {
		if t != nil && t.Validate() != nil {
			return historyInvalid("gap time")
		}
	}
	if g.FirstSequence != nil && g.LostEvents != nil && *g.LostEvents != *g.LastSequence-*g.FirstSequence+1 {
		return historyInvalid("gap count")
	}
	return nil
}

func (b HistoryBinding) Validate() error {
	if !historyText(b.InboundTag, 128) || b.InboundTag == "" || b.Label == "" ||
		!historyText(b.Label, 1024) || b.CredentialRef.Validate() != nil || !historyOne(b.Kind, "device", "shared") {
		return historyInvalid("binding")
	}
	switch b.LabelKind {
	case "xray_email":
		if b.Runtime != "xray" || b.Kind != "device" {
			return historyInvalid("xray binding")
		}
	case "hysteria_auth_id":
		if b.Runtime != "hysteria2" || b.Kind != "device" {
			return historyInvalid("hysteria binding")
		}
	case "wg_public_key":
		if b.Runtime != "wireguard" || b.Kind != "device" {
			return historyInvalid("wireguard binding")
		}
	case "shared_inbound":
		if !historyOne(b.Runtime, "xray", "hysteria2") || b.Kind != "shared" || b.Label != b.InboundTag {
			return historyInvalid("shared binding")
		}
	default:
		return historyInvalid("label kind")
	}
	return nil
}
