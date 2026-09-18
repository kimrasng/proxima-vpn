package xray

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
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
	switch req := v.(type) {
	case *queryStatsRequest:
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
	case *alterInboundRequest:
		return marshalAlterInboundRequest(req), nil
	case *onlineIPListRequest:
		var buf []byte
		if req.Name != "" {
			buf = appendTag(buf, 1, 2)
			buf = appendBytes(buf, []byte(req.Name))
		}
		return buf, nil
	default:
		return nil, fmt.Errorf("statsCodec: unsupported marshal type %T", v)
	}
}

func (statsCodec) Unmarshal(data []byte, v any) error {
	// AlterInboundResponse is an empty protobuf message, so an empty body is
	// the expected success case rather than a decode failure.
	if _, ok := v.(*alterInboundResponse); ok {
		return nil
	}

	if ips, ok := v.(*onlineIPListResponse); ok {
		return unmarshalOnlineIPList(data, ips)
	}

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
	statsServicePath   = "/xray.app.stats.command.StatsService"
	queryStatsMethod   = statsServicePath + "/QueryStats"
	onlineIPListMethod = statsServicePath + "/GetStatsOnlineIpList"
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
		// Accumulate: one device has a separate counter per protocol
		// (uuid@proxima, uuid@proxima-vmess, ...), all folding to this UUID.
		switch direction {
		case "uplink":
			ts.Upload += s.value
		case "downlink":
			ts.Download += s.value
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

// parseStatName extracts the device UUID and direction from a Xray stat name.
// Format: "user>>>{email}>>>traffic>>>{uplink|downlink}"
//
// Xray keys stats by client email, which the panel sets to "{uuid}@proxima"
// (plus -vmess/-trojan variants). The server matches on devices.xray_uuid, so
// the suffix must come off or nothing matches and traffic_used stays 0.
// Stripping it also sums a device's per-protocol counters into one total.
func parseStatName(name string) (uuid, direction string) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 {
		return "", ""
	}
	if parts[0] != "user" || parts[2] != "traffic" {
		return "", ""
	}
	email := parts[1]
	if at := strings.IndexByte(email, '@'); at >= 0 {
		email = email[:at]
	}
	if email == "" {
		return "", ""
	}
	return email, parts[3]
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

type onlineIPListRequest struct {
	Name string
}

type onlineIPListResponse struct {
	IPs map[string]int64
}

// unmarshalOnlineIPList decodes GetStatsOnlineIpListResponse, whose field 2 is
// a protobuf map<string,int64>. Maps are encoded as a repeated message with
// key in field 1 and value in field 2, so each entry is decoded as its own
// nested message rather than a single pair of scalars.
func unmarshalOnlineIPList(data []byte, resp *onlineIPListResponse) error {
	resp.IPs = map[string]int64{}
	for len(data) > 0 {
		field, wire, n := consumeTag(data)
		if n == 0 {
			return fmt.Errorf("statsCodec: bad online-ip tag")
		}
		data = data[n:]

		if field == 2 && wire == 2 {
			entry, m := consumeBytes(data)
			if m == 0 {
				return fmt.Errorf("statsCodec: bad online-ip entry length")
			}
			data = data[m:]

			ip, ts, err := parseOnlineIPEntry(entry)
			if err != nil {
				return err
			}
			if ip != "" {
				resp.IPs[ip] = ts
			}
			continue
		}

		skip, err := skipField(data, wire)
		if err != nil {
			return err
		}
		data = data[skip:]
	}
	return nil
}

func parseOnlineIPEntry(data []byte) (ip string, lastSeen int64, err error) {
	for len(data) > 0 {
		field, wire, n := consumeTag(data)
		if n == 0 {
			return "", 0, fmt.Errorf("statsCodec: bad map entry tag")
		}
		data = data[n:]

		switch {
		case field == 1 && wire == 2:
			b, m := consumeBytes(data)
			if m == 0 {
				return "", 0, fmt.Errorf("statsCodec: bad map key")
			}
			ip = string(b)
			data = data[m:]
		case field == 2 && wire == 0:
			v, m := binary.Uvarint(data)
			if m <= 0 {
				return "", 0, fmt.Errorf("statsCodec: bad map value")
			}
			lastSeen = int64(v)
			data = data[m:]
		default:
			skip, serr := skipField(data, wire)
			if serr != nil {
				return "", 0, serr
			}
			data = data[skip:]
		}
	}
	return ip, lastSeen, nil
}

// GetOnlineIPs returns the source IPs currently connected under each device
// UUID, mapped to the last time Xray saw them. Requires statsUserOnline on the
// client's policy level; without it Xray keeps no such map and this is empty.
// Xray skips loopback sources, so a same-host client reports nothing.
func (c *StatsClient) GetOnlineIPs(ctx context.Context, emails []string) (map[string]map[string]int64, error) {
	out := make(map[string]map[string]int64, len(emails))
	for _, email := range emails {
		req := &onlineIPListRequest{Name: "user>>>" + email + ">>>online"}
		resp := &onlineIPListResponse{}
		if err := c.conn.Invoke(ctx, onlineIPListMethod, req, resp); err != nil {
			// Xray has no online map for a client that has not connected since
			// it started, and reports that as NotFound. Skipping keeps one
			// never-used device from hiding every other device's addresses.
			if status.Code(err) == codes.NotFound {
				continue
			}
			return nil, fmt.Errorf("query online ips for %s: %w", email, err)
		}
		if len(resp.IPs) == 0 {
			continue
		}
		uuid := email
		if at := strings.IndexByte(uuid, '@'); at >= 0 {
			uuid = uuid[:at]
		}
		if out[uuid] == nil {
			out[uuid] = map[string]int64{}
		}
		for ip, ts := range resp.IPs {
			out[uuid][ip] = ts
		}
	}
	return out, nil
}
