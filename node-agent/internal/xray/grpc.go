package xray

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
)

func init() {
	encoding.RegisterCodec(statsCodec{})
}

// statsCodec is a minimal gRPC codec that hand-encodes the two protobuf
// messages we exchange with Xray's StatsService. We deliberately avoid pulling
// in Xray's generated protobufs (and the full protobuf runtime message
// machinery) by marshaling the protobuf wire format directly.
type statsCodec struct{}

func (statsCodec) Name() string { return "proto" }

func (statsCodec) Marshal(v any) ([]byte, error) {
	req, ok := v.(*queryStatsRequest)
	if !ok {
		return nil, fmt.Errorf("statsCodec: unsupported marshal type %T", v)
	}
	var buf []byte
	// field 1: pattern (string, wire type 2)
	if req.Pattern != "" {
		buf = appendTag(buf, 1, 2)
		buf = appendBytes(buf, []byte(req.Pattern))
	}
	// field 2: reset (bool, wire type 0)
	if req.Reset_ {
		buf = appendTag(buf, 2, 0)
		buf = binary.AppendUvarint(buf, 1)
	}
	return buf, nil
}

func (statsCodec) Unmarshal(data []byte, v any) error {
	resp, ok := v.(*queryStatsResponse)
	if !ok {
		return fmt.Errorf("statsCodec: unsupported unmarshal type %T", v)
	}
	resp.Stat = resp.Stat[:0]
	for len(data) > 0 {
		field, wire, n := consumeTag(data)
		if n == 0 {
			return fmt.Errorf("statsCodec: bad tag")
		}
		data = data[n:]
		// field 1 (repeated Stat), wire type 2
		if field == 1 && wire == 2 {
			b, m := consumeBytes(data)
			if m == 0 {
				return fmt.Errorf("statsCodec: bad stat length")
			}
			data = data[m:]
			s, err := parseStat(b)
			if err != nil {
				return err
			}
			resp.Stat = append(resp.Stat, s)
			continue
		}
		// skip unknown fields
		skip, err := skipField(data, wire)
		if err != nil {
			return err
		}
		data = data[skip:]
	}
	return nil
}

func parseStat(data []byte) (*statProto, error) {
	s := &statProto{}
	for len(data) > 0 {
		field, wire, n := consumeTag(data)
		if n == 0 {
			return nil, fmt.Errorf("statsCodec: bad stat tag")
		}
		data = data[n:]
		switch {
		case field == 1 && wire == 2: // name
			b, m := consumeBytes(data)
			if m == 0 {
				return nil, fmt.Errorf("statsCodec: bad name")
			}
			s.Name = string(b)
			data = data[m:]
		case field == 2 && wire == 0: // value
			val, m := binary.Uvarint(data)
			if m <= 0 {
				return nil, fmt.Errorf("statsCodec: bad value")
			}
			s.Value = int64(val)
			data = data[m:]
		default:
			skip, err := skipField(data, wire)
			if err != nil {
				return nil, err
			}
			data = data[skip:]
		}
	}
	return s, nil
}

func appendTag(buf []byte, field, wire int) []byte {
	return binary.AppendUvarint(buf, uint64(field)<<3|uint64(wire))
}

func appendBytes(buf, b []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(b)))
	return append(buf, b...)
}

func consumeTag(data []byte) (field, wire, n int) {
	tag, m := binary.Uvarint(data)
	if m <= 0 {
		return 0, 0, 0
	}
	return int(tag >> 3), int(tag & 0x7), m
}

func consumeBytes(data []byte) ([]byte, int) {
	l, m := binary.Uvarint(data)
	if m <= 0 || uint64(len(data)-m) < l {
		return nil, 0
	}
	return data[m : m+int(l)], m + int(l)
}

func skipField(data []byte, wire int) (int, error) {
	switch wire {
	case 0: // varint
		_, m := binary.Uvarint(data)
		if m <= 0 {
			return 0, fmt.Errorf("statsCodec: bad varint")
		}
		return m, nil
	case 2: // length-delimited
		l, m := binary.Uvarint(data)
		if m <= 0 || uint64(len(data)-m) < l {
			return 0, fmt.Errorf("statsCodec: bad length-delimited")
		}
		return m + int(l), nil
	case 5: // 32-bit
		return 4, nil
	case 1: // 64-bit
		return 8, nil
	default:
		return 0, fmt.Errorf("statsCodec: unsupported wire type %d", wire)
	}
}

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
		grpc.WithDefaultCallOptions(grpc.ForceCodec(statsCodec{})),
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
	req := &queryStatsRequest{Pattern: pattern, Reset_: reset}
	resp := &queryStatsResponse{}
	if err := c.conn.Invoke(ctx, queryStatsMethod, req, resp); err != nil {
		return nil, err
	}

	entries := make([]statEntry, 0, len(resp.Stat))
	for _, s := range resp.Stat {
		if s == nil {
			continue
		}
		entries = append(entries, statEntry{name: s.Name, value: s.Value})
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

type queryStatsRequest struct {
	Pattern string
	Reset_  bool
}

type statProto struct {
	Name  string
	Value int64
}

type queryStatsResponse struct {
	Stat []*statProto
}
