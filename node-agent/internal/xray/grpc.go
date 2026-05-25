package xray

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/protobuf/proto"
)

func init() {
	encoding.RegisterCodec(protoCodec{})
}

type protoCodec struct{}

func (protoCodec) Marshal(v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("protoCodec: cannot marshal non-proto message %T", v)
	}
	return proto.Marshal(m)
}

func (protoCodec) Unmarshal(data []byte, v any) error {
	m, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("protoCodec: cannot unmarshal into non-proto message %T", v)
	}
	return proto.Unmarshal(data, m)
}

func (protoCodec) Name() string { return "proto" }

type TrafficStat struct {
	UUID     string
	Upload   int64
	Download int64
}

type StatsClient struct {
	conn *grpc.ClientConn
	addr string
}

const (
	statsServicePath = "/xray.app.stats.command.StatsService"
	queryStatsMethod = statsServicePath + "/QueryStats"
)

func NewStatsClient(addr string) (*StatsClient, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(protoCodec{})),
	)
	if err != nil {
		return nil, fmt.Errorf("connect xray grpc: %w", err)
	}
	return &StatsClient{conn: conn, addr: addr}, nil
}

func (c *StatsClient) GetUserTraffic(ctx context.Context) ([]TrafficStat, error) {
	stats, err := c.queryStats(ctx, "user>>>", true)
	if err != nil {
		return nil, fmt.Errorf("query user traffic: %w", err)
	}

	trafficMap := make(map[string]*TrafficStat)
	for _, s := range stats {
		uuid, direction := parseStatName(s.name)
		if uuid == "" {
			continue
		}
		ts, ok := trafficMap[uuid]
		if !ok {
			ts = &TrafficStat{UUID: uuid}
			trafficMap[uuid] = ts
		}
		switch direction {
		case "uplink":
			ts.Upload = s.value
		case "downlink":
			ts.Download = s.value
		}
	}

	result := make([]TrafficStat, 0, len(trafficMap))
	for _, ts := range trafficMap {
		if ts.Upload > 0 || ts.Download > 0 {
			result = append(result, *ts)
		}
	}
	return result, nil
}

func (c *StatsClient) GetOnlineUsers(ctx context.Context) ([]string, error) {
	stats, err := c.queryStats(ctx, "user>>>", false)
	if err != nil {
		return nil, fmt.Errorf("query online users: %w", err)
	}

	seen := make(map[string]struct{})
	for _, s := range stats {
		uuid, direction := parseStatName(s.name)
		if uuid == "" || direction != "uplink" {
			continue
		}
		if s.value > 0 {
			seen[uuid] = struct{}{}
		}
	}

	users := make([]string, 0, len(seen))
	for uuid := range seen {
		users = append(users, uuid)
	}
	return users, nil
}

func (c *StatsClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type statEntry struct {
	name  string
	value int64
}

// queryStats invokes Xray's QueryStats gRPC method using protobuf wire encoding.
// Xray QueryStatsRequest: field 1 (string) = pattern, field 2 (bool) = reset
// Xray QueryStatsResponse: field 1 (repeated Stat): Stat.field1=name, Stat.field2=value
func (c *StatsClient) queryStats(ctx context.Context, pattern string, reset bool) ([]statEntry, error) {
	req := &queryStatsRequest{
		Pattern: proto.String(pattern),
		Reset_:  proto.Bool(reset),
	}

	resp := &queryStatsResponse{}
	if err := c.conn.Invoke(ctx, queryStatsMethod, req, resp); err != nil {
		return nil, err
	}

	entries := make([]statEntry, 0, len(resp.Stat))
	for _, s := range resp.Stat {
		if s == nil {
			continue
		}
		entries = append(entries, statEntry{name: s.GetName(), value: s.GetValue()})
	}
	return entries, nil
}

// parseStatName extracts UUID and direction from a Xray stat name.
// Format: "user>>>{uuid}>>>traffic>>>{uplink|downlink}"
func parseStatName(name string) (uuid, direction string) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 {
		return "", ""
	}
	if parts[0] != "user" || parts[2] != "traffic" {
		return "", ""
	}
	return parts[1], parts[3]
}

// Minimal proto message types mirroring xray.app.stats.command without importing Xray's proto.
type queryStatsRequest struct {
	Pattern *string `protobuf:"bytes,1,opt,name=pattern,proto3,oneof"`
	Reset_  *bool   `protobuf:"varint,2,opt,name=reset,proto3,oneof"`
}

func (q *queryStatsRequest) ProtoReflect() protoReflectIface { return nil }
func (q *queryStatsRequest) Reset()                          {}
func (q *queryStatsRequest) String() string                  { return fmt.Sprintf("%+v", *q) }
func (q *queryStatsRequest) ProtoMessage()                   {}

type statProto struct {
	Name  string `protobuf:"bytes,1,opt,name=name,proto3"`
	Value int64  `protobuf:"varint,2,opt,name=value,proto3"`
}

func (s *statProto) ProtoReflect() protoReflectIface { return nil }
func (s *statProto) Reset()                          {}
func (s *statProto) String() string                  { return fmt.Sprintf("%+v", *s) }
func (s *statProto) ProtoMessage()                   {}
func (s *statProto) GetName() string                 { return s.Name }
func (s *statProto) GetValue() int64                 { return s.Value }

type queryStatsResponse struct {
	Stat []*statProto `protobuf:"bytes,1,rep,name=stat,proto3"`
}

func (q *queryStatsResponse) ProtoReflect() protoReflectIface { return nil }
func (q *queryStatsResponse) Reset()                          {}
func (q *queryStatsResponse) String() string                  { return fmt.Sprintf("%+v", *q) }
func (q *queryStatsResponse) ProtoMessage()                   {}

type protoReflectIface = interface{}
