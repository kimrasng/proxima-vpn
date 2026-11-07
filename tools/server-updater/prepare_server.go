package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (c *Controller) servePreparation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		json.NewEncoder(w).Encode(c.preparationStatus())
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, `{"error":"invalid_request"}`, 400)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	var fields map[string]json.RawMessage
	var tag string
	if err != nil || strictJSON(data, &fields) != nil || len(fields) != 1 ||
		json.Unmarshal(fields["tag"], &tag) != nil || !releaseTagPattern.MatchString(tag) {
		http.Error(w, `{"error":"invalid_request"}`, 400)
		return
	}
	status, err := c.startPreparation(tag)
	if err != nil {
		if errors.Is(err, errBusy) {
			http.Error(w, `{"error":"blocked"}`, 409)
		} else {
			http.Error(w, `{"error":"not_authorized"}`, 403)
		}
		return
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(status)
}
