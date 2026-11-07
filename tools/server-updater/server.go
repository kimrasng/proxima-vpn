package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

type applyRequest struct {
	ReleaseID     string `json:"release_id"`
	Maintenance   bool   `json:"maintenance"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type preflightResponse struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// HTTP v1 is independent of the signed journal's storage version. Explicit
// fields keep trust policy, replay history and future journal metadata private.
// Both status and accepted apply return this same observation-only contract.
type statusResponse struct {
	Version   int                `json:"version"`
	Consumed  []string           `json:"consumed"`
	Operation *operationResponse `json:"operation"`
}

type operationResponse struct {
	Approval           approvalResponse `json:"approval"`
	Phase              string           `json:"phase"`
	Maintenance        bool             `json:"maintenance"`
	MigrationUncertain bool             `json:"migration_uncertain"`
	Error              string           `json:"error,omitempty"`
}

type approvalResponse struct {
	ID                 string         `json:"id"`
	From               imagesResponse `json:"from"`
	To                 imagesResponse `json:"to"`
	RollbackCompatible bool           `json:"rollback_compatible"`
}

type imagesResponse struct {
	API string `json:"api"`
	Web string `json:"web"`
}

func statusDTO(s State) statusResponse {
	response := statusResponse{Version: 1, Consumed: append([]string{}, s.Consumed...)}
	if op := s.Operation; op != nil {
		response.Operation = &operationResponse{
			Approval: approvalResponse{
				ID:                 op.Approval.ID,
				From:               imagesResponse{API: op.Approval.From.API, Web: op.Approval.From.Web},
				To:                 imagesResponse{API: op.Approval.To.API, Web: op.Approval.To.Web},
				RollbackCompatible: op.Approval.RollbackCompatible,
			},
			Phase: op.Phase, Maintenance: op.Maintenance,
			MigrationUncertain: op.MigrationUncertain, Error: op.Error,
		}
	}
	return response
}

func (c *Controller) statusJSON() ([]byte, error) {
	return json.Marshal(statusDTO(c.snapshot()))
}

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		http.Error(w, `{"error":"invalid_request"}`, 400)
		return
	}
	switch {
	case r.URL.Path == "/v1/prepare" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		c.servePreparation(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/status":
		b, err := c.statusJSON()
		if err != nil {
			http.Error(w, `{"error":"internal"}`, 500)
			return
		}
		w.Write(b)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/preflight":
		ok, reason := c.preflightReport()
		json.NewEncoder(w).Encode(preflightResponse{ok, reason})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/candidate":
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		candidate, err := c.candidate(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(struct {
				Error string `json:"error"`
			}{"candidate_unavailable"})
			return
		}
		json.NewEncoder(w).Encode(candidate)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apply":
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
		// Struct decoding matches JSON names case-insensitively. Decode the
		// exact keys instead, retaining strictJSON's duplicate/trailing checks.
		var fields map[string]json.RawMessage
		var req applyRequest
		if err != nil || strictJSON(b, &fields) != nil || (len(fields) != 2 && len(fields) != 3) ||
			json.Unmarshal(fields["release_id"], &req.ReleaseID) != nil ||
			json.Unmarshal(fields["maintenance"], &req.Maintenance) != nil ||
			!req.Maintenance || !namePattern.MatchString(req.ReleaseID) {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		if len(fields) == 3 && (json.Unmarshal(fields["payload_sha256"], &req.PayloadSHA256) != nil || !imageID.MatchString("sha256:"+req.PayloadSHA256)) {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		state, err := c.acceptCandidate(req.ReleaseID, req.PayloadSHA256)
		if err != nil {
			switch {
			case errors.Is(err, errInvalid):
				http.Error(w, `{"error":"invalid_request"}`, 400)
			case errors.Is(err, errBusy):
				http.Error(w, `{"error":"blocked"}`, 409)
			case errors.Is(err, errApproval):
				http.Error(w, `{"error":"not_approved"}`, 403)
			default:
				http.Error(w, `{"error":"persistence_failure"}`, 500)
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(statusDTO(state))
	default:
		http.Error(w, `{"error":"not_found"}`, 404)
	}
}

func listenSocket(path string, gid int) (net.Listener, error) {
	st, err := os.Lstat(path)
	if err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, errInvalid
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// umask is set to 0077 in main before any goroutines start.
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(path, 0, gid); err != nil {
		l.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0660); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func newServer(c *Controller) *http.Server {
	return &http.Server{Handler: c, ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
}
