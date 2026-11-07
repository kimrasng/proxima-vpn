package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

// Preparation is an observation, not a reservation. The host owns serialization
// with apply; do not cache candidates, queue starts, or retry uncertain writes.
func (h *ServerUpdateControlHandler) preparationResponse(req *http.Request, status int, data []byte, failure fiber.Map) (int, any) {
	posting := req.Method == http.MethodPost
	if posting && status != http.StatusAccepted {
		var hostError struct {
			Error string `json:"error"`
		}
		if serverUpdateObject(data, &hostError, "error") == nil && hostError.Error != "" &&
			map[int]string{400: "invalid_request", 403: "not_authorized", 409: "blocked"}[status] == hostError.Error {
			return status, hostError
		}
		return 502, failure
	}
	if !posting && status != http.StatusOK {
		return 503, failure
	}
	var result struct {
		Status    string          `json:"status"`
		Tag       string          `json:"tag,omitempty"`
		Candidate json.RawMessage `json:"candidate,omitempty"`
		Error     string          `json:"error,omitempty"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || json.Unmarshal(fields["status"], &result.Status) != nil {
		return 502, failure
	}
	keys := []string{"status", "tag"}
	switch result.Status {
	case "idle":
		keys = []string{"status"}
	case "preparing":
	case "prepared":
		keys = append(keys, "candidate")
	case "failed":
		keys = append(keys, "error")
	default:
		return 502, failure
	}
	if serverUpdateObject(data, &result, keys...) != nil ||
		(result.Status != "idle" && !serverUpdateTag.MatchString(result.Tag)) ||
		(result.Status == "failed" && result.Error != "preparation_failed") {
		return 502, failure
	}
	if posting {
		var input struct {
			Tag string `json:"tag"`
		}
		// NewRequest's bytes.Reader supplies GetBody without replaying HTTP.
		body, err := req.GetBody()
		if err != nil {
			return 502, failure
		}
		defer func() { _ = body.Close() }()
		if json.NewDecoder(body).Decode(&input) != nil || result.Status != "preparing" || result.Tag != input.Tag {
			return 502, failure
		}
	}
	if result.Status == "prepared" {
		var candidate struct {
			Status        string          `json:"status"`
			ReleaseID     string          `json:"release_id"`
			Sequence      uint64          `json:"sequence"`
			PayloadSHA256 string          `json:"payload_sha256"`
			To            json.RawMessage `json:"to"`
			ExpiresAt     int64           `json:"expires_at"`
		}
		now := h.now().Unix()
		if serverUpdateObject(result.Candidate, &candidate, "status", "release_id", "sequence", "payload_sha256", "to", "expires_at") != nil ||
			candidate.Status != "prepared" || !serverUpdateID.MatchString(candidate.ReleaseID) ||
			candidate.Sequence == 0 || candidate.Sequence > 1<<53-1 || !serverUpdateHash.MatchString(candidate.PayloadSHA256) ||
			candidate.ExpiresAt <= now || candidate.ExpiresAt > now+7*24*60*60 {
			return 502, failure
		}
		var images struct {
			API string `json:"api"`
			Web string `json:"web"`
		}
		if serverUpdateObject(candidate.To, &images, "api", "web") != nil || !serverUpdateImage.MatchString(images.API) || !serverUpdateImage.MatchString(images.Web) {
			return 502, failure
		}
	}
	return status, result
}
