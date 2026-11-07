package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyRequiresExactJSONFields(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"documented fields", `{"release_id":"release-one","maintenance":true}`, http.StatusAccepted},
		{"reordered fields", ` { "maintenance": true, "release_id": "release-one" } `, http.StatusAccepted},
		{"uppercase release", `{"RELEASE_ID":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"mixed case release", `{"Release_Id":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"uppercase maintenance", `{"release_id":"release-one","MAINTENANCE":true}`, http.StatusBadRequest},
		{"mixed case maintenance", `{"release_id":"release-one","Maintenance":true}`, http.StatusBadRequest},
		{"release alias", `{"releaseId":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"Go release name", `{"ReleaseID":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"release variant alongside exact", `{"release_id":"release-one","RELEASE_ID":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"maintenance variant alongside exact", `{"release_id":"release-one","maintenance":true,"Maintenance":true}`, http.StatusBadRequest},
		{"variant before exact", `{"Maintenance":false,"release_id":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"duplicate release", `{"release_id":"release-one","release_id":"release-one","maintenance":true}`, http.StatusBadRequest},
		{"duplicate maintenance", `{"release_id":"release-one","maintenance":true,"maintenance":true}`, http.StatusBadRequest},
		{"escaped duplicate", `{"release_id":"release-one","maintenance":true,"\u006daintenance":true}`, http.StatusBadRequest},
		{"missing release", `{"maintenance":true}`, http.StatusBadRequest},
		{"missing maintenance", `{"release_id":"release-one"}`, http.StatusBadRequest},
		{"null release", `{"release_id":null,"maintenance":true}`, http.StatusBadRequest},
		{"null maintenance", `{"release_id":"release-one","maintenance":null}`, http.StatusBadRequest},
		{"wrong release type", `{"release_id":1,"maintenance":true}`, http.StatusBadRequest},
		{"wrong maintenance type", `{"release_id":"release-one","maintenance":"true"}`, http.StatusBadRequest},
		{"null body", `null`, http.StatusBadRequest},
		{"array body", `[]`, http.StatusBadRequest},
		{"malformed body", `{"release_id":"release-one","maintenance":true`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, f, _ := fixture(t)
			r := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			c.ServeHTTP(w, r)
			if w.Code == http.StatusAccepted {
				waitOperation(t, c)
			}
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
			if tt.want == http.StatusBadRequest && (c.snapshot().Operation != nil || len(f.starts) != 0 || f.stops != 0) {
				t.Fatal("invalid request created an operation or mutated Docker")
			}
		})
	}
}
