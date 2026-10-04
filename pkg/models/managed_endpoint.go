package models

import (
	"net/netip"
	"time"
)

type RealitySNISource string

const (
	RealitySNISourceBackfill RealitySNISource = "backfill"
	RealitySNISourceAdmin    RealitySNISource = "admin"
)

type RealitySNIStatus string

const (
	RealitySNIValid         RealitySNIStatus = "valid"
	RealitySNIConflict      RealitySNIStatus = "conflict"
	RealitySNINotApplicable RealitySNIStatus = "not_applicable"
)

type RealitySNIErrorCode string

const (
	RealitySNINoCommonName     RealitySNIErrorCode = "no_common_name"
	RealitySNIInvalidName      RealitySNIErrorCode = "invalid_sni"
	RealitySNIListenerMismatch RealitySNIErrorCode = "listener_mismatch"
	RealitySNINotReality       RealitySNIErrorCode = "not_reality"
)

type NodeRealitySNI struct {
	NodeID    string               `json:"-" db:"id"`
	ClientSNI *string              `json:"-" db:"reality_client_sni"`
	Source    *RealitySNISource    `json:"-" db:"reality_sni_source"`
	Status    *RealitySNIStatus    `json:"-" db:"reality_sni_status"`
	ErrorCode *RealitySNIErrorCode `json:"-" db:"reality_sni_error_code"`
}

type ManagedDNSAction string

const (
	ManagedDNSPresent ManagedDNSAction = "present"
	ManagedDNSDelete  ManagedDNSAction = "delete"
)

type ManagedDNSStatus string

const (
	ManagedDNSPending  ManagedDNSStatus = "pending"
	ManagedDNSReady    ManagedDNSStatus = "ready"
	ManagedDNSConflict ManagedDNSStatus = "conflict"
	ManagedDNSError    ManagedDNSStatus = "error"
	ManagedDNSDeleting ManagedDNSStatus = "deleting"
	ManagedDNSDeleted  ManagedDNSStatus = "deleted"
)

type ManagedDNSErrorCode string

const (
	ManagedDNSProviderUnavailable  ManagedDNSErrorCode = "provider_unavailable"
	ManagedDNSProviderRejected     ManagedDNSErrorCode = "provider_rejected"
	ManagedDNSOwnershipConflict    ManagedDNSErrorCode = "ownership_conflict"
	ManagedDNSInvalidConfiguration ManagedDNSErrorCode = "invalid_configuration"
	ManagedDNSMismatch             ManagedDNSErrorCode = "dns_mismatch"
)

// ManagedEntryDNS is an internal persistence record, not a wire response.
type ManagedEntryDNS struct {
	ID                 string               `json:"-" db:"id"`
	OwnerNodeID        string               `json:"-" db:"owner_node_id"`
	NodeID             *string              `json:"-" db:"node_id"`
	Hostname           *string              `json:"-" db:"hostname"`
	DesiredIPv4        *netip.Addr          `json:"-" db:"desired_ipv4"`
	ObservedIPv4       *netip.Addr          `json:"-" db:"observed_ipv4"`
	DesiredAction      ManagedDNSAction     `json:"-" db:"desired_action"`
	DNSStatus          *ManagedDNSStatus    `json:"-" db:"dns_status"`
	ObservedAt         *time.Time           `json:"-" db:"observed_at"`
	ErrorCode          *ManagedDNSErrorCode `json:"-" db:"error_code"`
	CleanupRequestedAt *time.Time           `json:"-" db:"cleanup_requested_at"`
	CloudflareZoneID   *string              `json:"-" db:"cloudflare_zone_id"`
	OwnershipMarker    *string              `json:"-" db:"ownership_marker"`
	ProviderRecordID   *string              `json:"-" db:"provider_record_id"`
	Generation         int64                `json:"-" db:"generation"`
	RetryCount         int32                `json:"-" db:"retry_count"`
	NextAttemptAt      *time.Time           `json:"-" db:"next_attempt_at"`
	CreatedAt          time.Time            `json:"-" db:"created_at"`
	UpdatedAt          time.Time            `json:"-" db:"updated_at"`
}
