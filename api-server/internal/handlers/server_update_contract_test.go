package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerUpdateControlActualSignedHostContract(t *testing.T) {
	// The updater is a standalone main module. Run its real handler fixture in
	// that module rather than import main or duplicate its serialized DTO here.
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-race", "-count=1", "-run", "^Test(HTTP(CandidateContractResponses|ContractResponses)|PublisherPreparationAndHashBoundApplyWithoutManualApproval|PreparationDoesNotQueueOrRaceApply|ApplyRejectsMissingOrChangedPayloadIncludingIdempotence|PreparedCandidateSurvivesRestartWithFullReverification|PublisherHashMatchesSignedPayload|PreparationEndpointDefaultsToManualAuthorization|PreparationStatusIsSeparateFromApplyJournal|PrepareRejectsRequestAuthority)$", ".")
	cmd.Dir = filepath.Join("..", "..", "..", "tools", "server-updater")
	cmd.Env = append(os.Environ(), "GOWORK=off", "PROXIMA_UPDATER_CONTRACT_OUTPUT="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual host fixture: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "responses.json"))
	if err != nil {
		t.Fatal(err)
	}
	var responses []struct {
		Name   string          `json:"name"`
		Path   string          `json:"path"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &responses); err != nil {
		t.Fatal(err)
	}
	wantPhases := map[string]string{
		"signed idle": "", "legacy idle": "", "accepted": "accepted",
		"recovery accepted": "accepted", "succeeded": "succeeded",
		"idempotent succeeded": "succeeded", "recovery": "manual_recovery",
		"idempotent recovery": "manual_recovery",
		"rejected":            "rejected", "rolled_back": "rolled_back", "uncertain recovery": "manual_recovery",
	}
	if len(responses) != len(wantPhases) {
		t.Fatalf("missing host scenarios: got %d, want %d", len(responses), len(wantPhases))
	}
	a, payload := serverUpdateTestIdentity(t)
	for _, response := range responses {
		phase, exists := wantPhases[response.Name]
		if !exists {
			t.Fatalf("unexpected or duplicate scenario %q", response.Name)
		}
		delete(wantPhases, response.Name)
		t.Run(response.Name, func(t *testing.T) {
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.Status)
				if _, err := w.Write(response.Body); err != nil {
					t.Error(err)
				}
			}))
			method, body, want := "GET", "", http.StatusOK
			if response.Path == "/v1/apply" {
				method, body, want = "POST", payload, http.StatusAccepted
			}
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), method, strings.TrimPrefix(response.Path, "/v1"), body, a.id, "owner", nil)
			if status != want || result["version"] != float64(1) {
				t.Fatalf("actual host response rejected: %d %v; host=%s", status, result, response.Body)
			}
			if phase == "" {
				if result["operation"] != nil {
					t.Fatalf("idle host has operation: %v", result)
				}
				return
			}
			op, ok := result["operation"].(map[string]any)
			if !ok || op["phase"] != phase {
				t.Fatalf("operation changed: %v", result)
			}
			uncertain := phase == "succeeded" || phase == "rolled_back" || response.Name == "uncertain recovery"
			if op["maintenance"] != (phase == "manual_recovery") || op["migration_uncertain"] != uncertain {
				t.Fatalf("maintenance or migration uncertainty changed: %v", result)
			}
			wantError := ""
			switch phase {
			case "manual_recovery":
				wantError = "recovery_stop_unverified"
			case "rejected":
				wantError = "preflight_failed"
			case "rolled_back":
				wantError = "apply_failed"
			}
			gotError, _ := op["error"].(string)
			if gotError != wantError {
				t.Fatalf("recovery semantics lost: %v", result)
			}
		})
	}
	testServerUpdateCandidateContract(t, dir, a)
	testServerUpdatePreparationContract(t, dir, a, payload)
}
