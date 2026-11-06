package nodeprov

import (
	"bytes"
	"encoding/json"
)

const HistoryMaxControlLine = 1024 * 1024

type HistoryHello struct {
	Op                     string       `json:"op"`
	SchemaVersion          int          `json:"schema_version"`
	ProducerID             string       `json:"producer_id"`
	ObserverBootID         HistoryUUID  `json:"observer_boot_id"`
	RuntimeBuild           string       `json:"runtime_build"`
	Capabilities           []string     `json:"capabilities"`
	FirstAvailableSequence HistoryU64   `json:"first_available_sequence"`
	LastSequence           HistoryU64   `json:"last_sequence"`
	Enabled                bool         `json:"enabled"`
	ConfigRevision         HistoryU64   `json:"config_revision"`
	CollectionEpoch        *HistoryUUID `json:"collection_epoch"`
}
type HistoryApply struct {
	Op              string           `json:"op"`
	ConfigRevision  HistoryU64       `json:"config_revision"`
	CollectionEpoch *HistoryUUID     `json:"collection_epoch"`
	Enabled         bool             `json:"enabled"`
	Bindings        []HistoryBinding `json:"bindings"`
}
type HistoryApplied struct {
	Op              string       `json:"op"`
	ConfigRevision  HistoryU64   `json:"config_revision"`
	CollectionEpoch *HistoryUUID `json:"collection_epoch"`
	Enabled         bool         `json:"enabled"`
	BarrierSequence HistoryU64   `json:"barrier_sequence"`
}
type HistoryResume struct {
	Op             string      `json:"op"`
	ObserverBootID HistoryUUID `json:"observer_boot_id"`
	AfterSequence  HistoryU64  `json:"after_sequence"`
}
type HistoryAck struct {
	Op              string      `json:"op"`
	ObserverBootID  HistoryUUID `json:"observer_boot_id"`
	ThroughSequence HistoryU64  `json:"through_sequence"`
}
type HistoryError struct {
	Op             string     `json:"op"`
	ConfigRevision HistoryU64 `json:"config_revision"`
	Code           string     `json:"code"`
}

func (e *HistoryError) Error() string { return "history control: " + e.Code }

// HistoryControl is a closed set of private control frames.
type HistoryControl interface{ historyControl() }

func (HistoryHello) historyControl()   {}
func (HistoryApply) historyControl()   {}
func (HistoryApplied) historyControl() {}
func (HistoryResume) historyControl()  {}
func (HistoryAck) historyControl()     {}
func (HistoryError) historyControl()   {}

func DecodeHistoryControl(line []byte) (HistoryControl, error) {
	if len(line) == 0 || len(line) > HistoryMaxControlLine || line[len(line)-1] != '\n' ||
		bytes.ContainsAny(line[:len(line)-1], "\r\n") {
		return nil, historyInvalid("control framing")
	}
	var tag struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(line, &tag); err != nil {
		return nil, historyInvalid("control JSON")
	}
	switch tag.Op {
	case "hello":
		var v HistoryHello
		if err := decodeHistory(line, &v); err != nil {
			return nil, err
		}
		if v.SchemaVersion != 1 || !historyASCII(v.ProducerID) || !historyASCII(v.RuntimeBuild) ||
			v.ObserverBootID.Validate() != nil || !HistoryCapabilitiesValid(v.Capabilities) ||
			(v.Enabled && v.CollectionEpoch == nil) || v.FirstAvailableSequence == 0 {
			return nil, historyInvalid("hello")
		}
		return v, nil
	case "apply":
		var v HistoryApply
		if err := decodeHistory(line, &v); err != nil {
			return nil, err
		}
		if v.Enabled && v.CollectionEpoch == nil {
			return nil, historyInvalid("apply epoch")
		}
		for _, b := range v.Bindings {
			if err := b.Validate(); err != nil {
				return nil, err
			}
		}
		return v, nil
	case "applied":
		var v HistoryApplied
		if err := decodeHistory(line, &v); err != nil {
			return nil, err
		}
		if v.Enabled && v.CollectionEpoch == nil {
			return nil, historyInvalid("applied epoch")
		}
		return v, nil
	case "resume":
		var v HistoryResume
		if err := decodeHistory(line, &v); err != nil {
			return nil, err
		}
		return v, nil
	case "ack":
		var v HistoryAck
		if err := decodeHistory(line, &v); err != nil {
			return nil, err
		}
		return v, nil
	case "error":
		var v HistoryError
		if err := decodeHistory(line, &v); err != nil {
			return nil, err
		}
		if !historyOne(v.Code, "revision_stale", "revision_conflict", "binding_invalid", "epoch_invalid", "unsupported", "export_unavailable") {
			return nil, historyInvalid("error code")
		}
		return v, nil
	default:
		return nil, historyInvalid("control op")
	}
}

func HistoryCapabilitiesValid(caps []string) bool {
	for i, c := range caps {
		if !historyOne(c, "auth_transport", "tcp_flow", "udp_destination", "mux", "xudp", "hot_toggle", "activity_window", "inner_ip_bytes") ||
			(i > 0 && caps[i-1] >= c) {
			return false
		}
	}
	return caps != nil
}
