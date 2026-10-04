package handlers

import (
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

const nodeEndpointSelection = `, med.hostname, med.dns_status, med.error_code,
    nodes.reality_client_sni, nodes.reality_sni_status, nodes.reality_sni_error_code
    FROM nodes LEFT JOIN managed_entry_dns med ON med.node_id = nodes.id`

type nodeEndpointFields struct {
	EntryHostname       *string `json:"entry_hostname"`
	EntryDNSStatus      *string `json:"entry_dns_status"`
	EntryDNSErrorCode   *string `json:"entry_dns_error_code"`
	RealityClientSNI    *string `json:"reality_client_sni"`
	RealitySNIStatus    *string `json:"reality_sni_status"`
	RealitySNIErrorCode *string `json:"reality_sni_error_code"`
}

func (f *nodeEndpointFields) scanTargets() []any {
	return []any{&f.EntryHostname, &f.EntryDNSStatus, &f.EntryDNSErrorCode,
		&f.RealityClientSNI, &f.RealitySNIStatus, &f.RealitySNIErrorCode}
}

type nodeSNIInput struct {
	Present bool
	Value   string
}

func parseNodeEndpointInput(body []byte) (nodeSNIInput, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nodeSNIInput{}, err
	}
	for _, key := range []string{"entry_hostname", "entry_dns_status", "entry_dns_error_code"} {
		if _, present := fields[key]; present {
			return nodeSNIInput{}, fiber.NewError(fiber.StatusBadRequest, "entry DNS fields are read-only")
		}
	}
	raw, present := fields["reality_client_sni"]
	if !present {
		return nodeSNIInput{}, nil
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil || *value == "" {
		return nodeSNIInput{}, fiber.NewError(fiber.StatusBadRequest, "reality_client_sni must be a non-empty hostname")
	}
	return nodeSNIInput{Present: true, Value: *value}, nil
}

func nodeSNIErrorResponse(c *fiber.Ctx, err error) error {
	var sniError *services.RealitySNIError
	if errors.As(err, &sniError) {
		switch sniError.Kind {
		case services.RealitySNIMalformed:
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid reality_client_sni"})
		case services.RealitySNIMissingNode:
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node not found"})
		case services.RealitySNINoListener, services.RealitySNIListenerConflict,
			services.RealitySNIInvalidListener, services.RealitySNIMismatch:
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Reality SNI is incompatible with node listeners"})
		}
	}
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update node"})
}
