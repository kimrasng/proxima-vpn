package main

import (
	"context"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"log"
	"time"
)

// activeUUIDReportLoop reports admitted open associations, not packet activity.
// A missing report is unknown to the controller and never releases a slot.
func activeUUIDReportLoop(ctx context.Context, api *client.APIClient, server *deviceegress.Server) {
	generation := uuid.NewString()
	for ctx.Err() == nil {
		if err := api.BeginActiveUUIDGeneration(ctx, generation); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var sequence int64
	send := func() {
		sequence++
		ids := server.ActiveAdmittedUUIDs()
		if ids == nil {
			ids = []string{}
		}
		err := api.ReportActiveUUIDs(ctx, client.ActiveUUIDReport{Generation: generation, Sequence: sequence, UUIDs: ids})
		if err != nil {
			log.Printf("active UUID report: %v", err)
		}
	}
	send()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
