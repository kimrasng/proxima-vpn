package nodeprov

// HistoryEvent is the v1 full-snapshot wire contract. Nullable members are
// deliberately not omitempty. DecodeHistoryEvent is the untrusted JSON boundary.
type HistoryEvent struct {
	SchemaVersion   int                 `json:"schema_version"`
	EventID         string              `json:"event_id"`
	ObserverBootID  HistoryUUID         `json:"observer_boot_id"`
	Sequence        HistoryU64          `json:"sequence"`
	ProducerID      string              `json:"producer_id"`
	Runtime         string              `json:"runtime"`
	RuntimeBuild    string              `json:"runtime_build"`
	ConfigRevision  HistoryU64          `json:"config_revision"`
	CollectionEpoch HistoryUUID         `json:"collection_epoch"`
	EventTime       HistoryTime         `json:"event_time"`
	MonotonicNS     HistoryU64          `json:"monotonic_ns"`
	Kind            string              `json:"kind"`
	TunnelID        *string             `json:"tunnel_id"`
	FlowID          *string             `json:"flow_id"`
	Protocol        *string             `json:"protocol"`
	InboundTag      *string             `json:"inbound_tag"`
	Auth            HistoryAuth         `json:"auth"`
	Peer            *HistoryPeer        `json:"peer"`
	Destination     *HistoryDestination `json:"destination"`
	Bytes           HistoryBytes        `json:"bytes"`
	Lifecycle       HistoryLifecycle    `json:"lifecycle"`
	Quality         HistoryQuality      `json:"quality"`
	Boundary        *HistoryBoundary    `json:"boundary"`
	Gap             *HistoryGap         `json:"gap"`
}

type HistoryAuth struct {
	Kind            string       `json:"kind"`
	CredentialRef   *HistoryUUID `json:"credential_ref"`
	BindingRevision *HistoryU64  `json:"binding_revision"`
}

type HistoryPeer struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type HistoryDestination struct {
	RequestedHost *string `json:"requested_host"`
	RequestedPort *int    `json:"requested_port"`
	Transport     string  `json:"transport"`
	EffectiveHost *string `json:"effective_host"`
	ObservedIP    *string `json:"observed_ip"`
	ObservedPort  *int    `json:"observed_port"`
}

type HistoryBytes struct {
	Layer    string      `json:"layer"`
	Upload   *HistoryU64 `json:"upload"`
	Download *HistoryU64 `json:"download"`
	Final    bool        `json:"final"`
}

type HistoryLifecycle struct {
	Scope          string       `json:"scope"`
	StartedAt      *HistoryTime `json:"started_at"`
	LastObservedAt *HistoryTime `json:"last_observed_at"`
	EndedAt        *HistoryTime `json:"ended_at"`
	PartialStart   bool         `json:"partial_start"`
	EndReason      *string      `json:"end_reason"`
	EndSemantics   *string      `json:"end_semantics"`
}

type HistoryQuality struct {
	Identity     string `json:"identity"`
	Parent       string `json:"parent"`
	Destination  string `json:"destination"`
	Bytes        string `json:"bytes"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Completeness string `json:"completeness"`
}

type HistoryBoundary struct {
	Action        string     `json:"action"`
	PriorRevision HistoryU64 `json:"prior_revision"`
}

type HistoryGap struct {
	ProducerID     string       `json:"producer_id"`
	ObserverBootID HistoryUUID  `json:"observer_boot_id"`
	FirstSequence  *HistoryU64  `json:"first_sequence"`
	LastSequence   *HistoryU64  `json:"last_sequence"`
	FromTime       *HistoryTime `json:"from_time"`
	ToTime         *HistoryTime `json:"to_time"`
	LostEvents     *HistoryU64  `json:"lost_events"`
	Reason         string       `json:"reason"`
}

// HistoryBinding is private control data, never part of an exported event.
type HistoryBinding struct {
	Runtime         string      `json:"runtime"`
	InboundTag      string      `json:"inbound_tag"`
	LabelKind       string      `json:"label_kind"`
	Label           string      `json:"label"`
	CredentialRef   HistoryUUID `json:"credential_ref"`
	Kind            string      `json:"kind"`
	BindingRevision HistoryU64  `json:"binding_revision"`
}

const HistoryMaxEventLine = 16 * 1024
