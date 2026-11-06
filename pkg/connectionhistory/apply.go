package connectionhistory

import (
	"bytes"
	"encoding/json"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type bindingKey struct{ inbound, kind, label string }

func controlError(revision nodeprov.HistoryU64, code string) error {
	return &nodeprov.HistoryError{Op: "error", ConfigRevision: revision, Code: code}
}

func (o *Observer) Apply(a nodeprov.HistoryApply) (nodeprov.HistoryApplied, error) {
	fail := func(code string) (nodeprov.HistoryApplied, error) {
		return nodeprov.HistoryApplied{}, controlError(a.ConfigRevision, code)
	}
	if a.Op != "apply" || a.Bindings == nil {
		return fail("binding_invalid")
	}
	bindings := make(map[bindingKey]nodeprov.HistoryAuth, len(a.Bindings))
	for _, b := range a.Bindings {
		if b.Validate() != nil || b.Runtime != o.options.Runtime {
			return fail("binding_invalid")
		}
		key := bindingKey{b.InboundTag, b.LabelKind, b.Label}
		if _, exists := bindings[key]; exists {
			return fail("binding_invalid")
		}
		bindings[key] = nodeprov.HistoryAuth{Kind: b.Kind, CredentialRef: ptr(b.CredentialRef), BindingRevision: ptr(b.BindingRevision)}
	}
	if (a.Enabled && a.CollectionEpoch == nil) || (a.CollectionEpoch != nil && a.CollectionEpoch.Validate() != nil) {
		return fail("epoch_invalid")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return nodeprov.HistoryApplied{}, err
	}
	desired, err := nodeprov.CanonicalHistoryJSON(raw)
	if err != nil {
		return nodeprov.HistoryApplied{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if a.ConfigRevision < o.revision {
		return fail("revision_stale")
	}
	if o.desired != nil && a.ConfigRevision == o.revision {
		if !bytes.Equal(desired, o.desired) {
			return fail("revision_conflict")
		}
		return copyApplied(o.applied), nil
	}
	if o.enabled || !a.Enabled {
		if !sameEpoch(a.CollectionEpoch, o.epoch) {
			return fail("epoch_invalid")
		}
	} else if _, used := o.usedEpochs[*a.CollectionEpoch]; used {
		return fail("epoch_invalid")
	}
	prior := o.revision
	wasEnabled := o.enabled
	if wasEnabled && !a.Enabled {
		// These nonterminal samples belong to the active interval, not the
		// disabling revision installed for the boundary and applied response.
		for _, f := range o.sortedFlowsLocked() {
			if err := f.emitLocked("flow_update", nil); err != nil {
				return nodeprov.HistoryApplied{}, err
			}
		}
		for _, t := range o.sortedTunnelsLocked() {
			if err := t.emitLocked("tunnel_update", nil); err != nil {
				return nodeprov.HistoryApplied{}, err
			}
		}
	}
	o.revision, o.bindings = a.ConfigRevision, bindings
	o.epoch = copyPtr(a.CollectionEpoch)
	if a.Enabled && !wasEnabled {
		o.enabled = true
		o.usedEpochs[*o.epoch] = struct{}{}
		if err := o.boundaryLocked("enabled", prior); err != nil {
			return nodeprov.HistoryApplied{}, err
		}
		for _, t := range o.sortedTunnelsLocked() {
			t.beginEpochLocked(true)
			if err := t.emitLocked("tunnel_start", nil); err != nil {
				return nodeprov.HistoryApplied{}, err
			}
		}
		for _, f := range o.sortedFlowsLocked() {
			f.beginEpochLocked(true)
			if err := f.emitLocked("flow_start", nil); err != nil {
				return nodeprov.HistoryApplied{}, err
			}
		}
	} else if wasEnabled && !a.Enabled {
		if err := o.boundaryLocked("disabled", prior); err != nil {
			return nodeprov.HistoryApplied{}, err
		}
		o.enabled = false
		for _, f := range o.flows {
			f.upload, f.download = 0, 0
		}
		for s := range o.scopes {
			s.partial = true
			s.resetLocked()
		}
	} else if wasEnabled {
		if err := o.boundaryLocked("reconfigured", prior); err != nil {
			return nodeprov.HistoryApplied{}, err
		}
	}
	o.desired = desired
	o.applied = nodeprov.HistoryApplied{
		Op: "applied", ConfigRevision: o.revision, CollectionEpoch: copyPtr(o.epoch),
		Enabled: o.enabled, BarrierSequence: o.sequence,
	}
	return copyApplied(o.applied), nil
}

func sameEpoch(a, b *nodeprov.HistoryUUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func copyApplied(a nodeprov.HistoryApplied) nodeprov.HistoryApplied {
	a.CollectionEpoch = copyPtr(a.CollectionEpoch)
	return a
}
func (o *Observer) boundaryLocked(action string, prior nodeprov.HistoryU64) error {
	e := o.baseLocked("collection_boundary")
	e.Boundary = &nodeprov.HistoryBoundary{Action: action, PriorRevision: prior}
	return o.emitLocked(e)
}

func (o *Observer) resolveLocked(info TunnelInfo) nodeprov.HistoryAuth {
	auth, ok := o.bindings[bindingKey{info.InboundTag, info.LabelKind, info.Label}]
	if !ok {
		return nodeprov.HistoryAuth{Kind: "unknown"}
	}
	return auth
}
