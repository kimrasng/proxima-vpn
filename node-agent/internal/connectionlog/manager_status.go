package connectionlog

import (
	"reflect"
	"sort"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type ProducerStatus struct {
	ProducerID          string                `json:"producer_id"`
	ObserverBootID      *nodeprov.HistoryUUID `json:"observer_boot_id"`
	RuntimeBuild        string                `json:"runtime_build"`
	Capabilities        []string              `json:"capabilities"`
	AppliedRevision     nodeprov.HistoryU64   `json:"applied_revision"`
	LastDurableSequence nodeprov.HistoryU64   `json:"last_durable_sequence"`
	ErrorCode           string                `json:"error_code"`
}

type Status struct {
	DesiredRevision   nodeprov.HistoryU64   `json:"desired_revision"`
	AppliedRevision   nodeprov.HistoryU64   `json:"applied_revision"`
	CollectionEpoch   *nodeprov.HistoryUUID `json:"collection_epoch"`
	Enabled           bool                  `json:"enabled"`
	State             string                `json:"state"`
	AppliedAt         *nodeprov.HistoryTime `json:"applied_at"`
	LastConfigFetchAt *nodeprov.HistoryTime `json:"last_config_fetch_at"`
	QueueBytes        nodeprov.HistoryU64   `json:"queue_bytes"`
	DroppedEvents     nodeprov.HistoryU64   `json:"dropped_events"`
	ErrorCode         string                `json:"error_code"`
	Producers         []ProducerStatus      `json:"producers"`
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.out.mu.Lock()
	defer m.out.mu.Unlock()
	s := Status{State: "pending",
		QueueBytes: nodeprov.HistoryU64(m.out.bytes), DroppedEvents: m.out.disk.Dropped,
		Producers: []ProducerStatus{}}
	if m.fetched != nil {
		at := *m.fetched
		s.LastConfigFetchAt = &at
	}
	if m.desired != nil {
		s.DesiredRevision = m.desired.Revision
	}
	allApplied := m.desired != nil && len(m.workers) > 0
	first := true
	conflict := false
	for id, w := range m.workers {
		cp := m.out.disk.Sources[id]
		p := ProducerStatus{ProducerID: id, RuntimeBuild: cp.Hello.RuntimeBuild,
			Capabilities: append([]string{}, cp.Hello.Capabilities...), LastDurableSequence: cp.Through,
			ErrorCode: w.err}
		if p.ErrorCode == "" {
			p.ErrorCode = cp.GapCode
		}
		if cp.Hello.ObserverBootID != "" {
			boot := cp.Hello.ObserverBootID
			p.ObserverBootID = &boot
		}
		applied := nodeprov.HistoryApplied{}
		if cp.Applied != nil {
			applied = *cp.Applied
			p.AppliedRevision = applied.ConfigRevision
		}
		if first || p.AppliedRevision < s.AppliedRevision {
			s.AppliedRevision, s.Enabled = p.AppliedRevision, applied.Enabled
			s.CollectionEpoch, s.AppliedAt, conflict = nil, nil, false
			if applied.CollectionEpoch != nil {
				epoch := *applied.CollectionEpoch
				s.CollectionEpoch = &epoch
			}
		} else if p.AppliedRevision == s.AppliedRevision &&
			(applied.Enabled != s.Enabled || !reflect.DeepEqual(applied.CollectionEpoch, s.CollectionEpoch)) {
			conflict = true
		}
		if p.AppliedRevision == s.AppliedRevision && cp.AppliedAt != nil &&
			(s.AppliedAt == nil || *cp.AppliedAt > *s.AppliedAt) {
			at := *cp.AppliedAt
			s.AppliedAt = &at
		}
		first = false
		if cp.Applied == nil || m.desired == nil || cp.Applied.ConfigRevision != m.desired.Revision ||
			cp.Applied.Enabled != m.desired.Enabled {
			allApplied = false
		}
		if cp.Applied != nil && m.desired != nil && !reflect.DeepEqual(cp.Applied.CollectionEpoch, m.desired.CollectionEpoch) {
			allApplied = false
		}
		if p.ErrorCode != "" {
			allApplied = false
			if s.State != "unsupported" {
				s.ErrorCode, s.State = p.ErrorCode, "degraded"
			}
			if w.err == "unsupported" {
				s.ErrorCode, s.State = "unsupported", "unsupported"
			}
		}
		s.Producers = append(s.Producers, p)
	}
	sort.Slice(s.Producers, func(i, j int) bool { return s.Producers[i].ProducerID < s.Producers[j].ProducerID })
	if s.AppliedRevision == 0 || conflict {
		s.AppliedRevision, s.Enabled, s.CollectionEpoch, s.AppliedAt = 0, false, nil, nil
	}
	if allApplied {
		s.Enabled = m.desired.Enabled
		s.State = "off"
		if s.Enabled {
			s.State = "collecting"
		}
	}
	if m.pollErr != "" {
		s.ErrorCode = m.pollErr
	}
	if m.uploadErr != "" || len(m.out.disk.Losses) > 0 {
		s.State, s.ErrorCode = "degraded", "history_delivery_degraded"
	}
	if conflict {
		s.State, s.ErrorCode = "error", "applied_conflict"
	}
	return s
}
