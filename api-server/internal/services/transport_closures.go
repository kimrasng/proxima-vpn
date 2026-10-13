package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

// QueueTransportClosure requests an exact, generation-fenced transport close.
// This is not a credential ban: other transports sharing the UUID survive.
func QueueTransportClosure(ctx context.Context, rdb *redis.Client, nodeID string, connection nodeprov.TunnelConnection) (string, error) {
	if err := connection.Validate(); err != nil {
		return "", err
	}
	command := nodeprov.TransportClosure{ID: uuid.NewString(), Connection: connection}
	data, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	key := "node:" + nodeID + ":transport_closures"
	pipe := rdb.TxPipeline()
	pipe.HSet(ctx, key, command.ID, data)
	pipe.Expire(ctx, key, time.Minute)
	_, err = pipe.Exec(ctx)
	return command.ID, err
}

func PendingTransportClosures(ctx context.Context, rdb *redis.Client, nodeID string) ([]nodeprov.TransportClosure, error) {
	if rdb == nil {
		return nil, fmt.Errorf("transport closure storage unavailable")
	}
	values, err := rdb.HVals(ctx, "node:"+nodeID+":transport_closures").Result()
	if err != nil {
		return nil, err
	}
	out := make([]nodeprov.TransportClosure, 0, len(values))
	for _, value := range values {
		var command nodeprov.TransportClosure
		if err := json.Unmarshal([]byte(value), &command); err != nil {
			return nil, err
		}
		if err := command.Connection.Validate(); err != nil {
			return nil, err
		}
		out = append(out, command)
	}
	return out, nil
}
