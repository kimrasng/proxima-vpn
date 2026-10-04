// Package devicebandwidth defines the node-local egress and central budget contract.
package devicebandwidth

const (
	ListenAddress  = "127.0.0.1:10086"
	Port           = 10086
	OutboundPrefix = "device-egress-"
	MaxChunk       = 32768
)

type Direction string

const (
	Upload   Direction = "upload"
	Download Direction = "download"
)

type PermitRequest struct {
	DeviceUUID string    `json:"device_uuid"`
	Direction  Direction `json:"direction"`
	Bytes      int       `json:"bytes"`
}

type PermitResponse struct {
	Allowed      bool   `json:"allowed"`
	RetryAfterMS int    `json:"retry_after_ms"`
	DeniedReason string `json:"denied_reason,omitempty"`
}

type Credential struct {
	UUID     string
	Password string
}
