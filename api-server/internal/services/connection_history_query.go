package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type ConnectionHistoryQuery struct {
	NodeID            string
	UserID            string
	Epoch             string
	TunnelID          string
	EntityID          string
	Destination       string
	DestinationPrefix string
	Protocol          string
	Status            string
	From              *time.Time
	To                *time.Time
	Cursor            string
	Limit             int
}
type ConnectionHistoryRow struct {
	NodeID            string                        `json:"node_id"`
	NodeName          string                        `json:"node_name"`
	UserID            *string                       `json:"user_id"`
	DeviceID          *string                       `json:"device_id"`
	UserSnapshot      *string                       `json:"user_snapshot"`
	DeviceSnapshot    *string                       `json:"device_snapshot"`
	FirstObservedAt   time.Time                     `json:"first_observed_at"`
	ObservationStatus string                        `json:"observation_status"`
	Event             nodeprov.HistoryEvent         `json:"event"`
	GapCoverage       []json.RawMessage             `json:"gap_coverage"`
	ChildTotals       []ConnectionHistoryLayerTotal `json:"child_totals,omitempty"`
}
type ConnectionHistoryPage struct {
	Items      []ConnectionHistoryRow `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}
type historyCursor struct {
	First               time.Time
	Node, Epoch, Entity string
}
type ConnectionHistoryLayerTotal struct {
	Layer             string `json:"layer"`
	Upload            string `json:"upload"`
	Download          string `json:"download"`
	CompleteIntervals int64  `json:"complete_intervals"`
	PartialIntervals  int64  `json:"partial_intervals"`
	BoundaryPolicy    string `json:"boundary_policy"`
}

func (s *ConnectionHistoryService) Detail(ctx context.Context, flows bool, node, epoch, entity string) (ConnectionHistoryRow, error) {
	if node == "" || epoch == "" || entity == "" || len(entity) > 128 {
		return ConnectionHistoryRow{}, historyError(400, "invalid_entity")
	}
	page, err := s.Query(ctx, flows, ConnectionHistoryQuery{NodeID: node, Epoch: epoch, EntityID: entity, Limit: 1})
	if err != nil {
		return ConnectionHistoryRow{}, err
	}
	if len(page.Items) == 0 {
		return ConnectionHistoryRow{}, historyError(404, "history_not_found")
	}
	return page.Items[0], nil
}

const historyQueryJoin = ` LEFT JOIN connection_log_controls c ON c.node_origin=p.node_origin
 LEFT JOIN connection_log_bindings b ON b.node_origin=p.node_origin AND b.credential_ref::text=p.payload->'auth'->>'credential_ref' `

func (s *ConnectionHistoryService) Query(ctx context.Context, flows bool, q ConnectionHistoryQuery) (ConnectionHistoryPage, error) {
	page := ConnectionHistoryPage{Items: []ConnectionHistoryRow{}}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 {
		return page, historyError(400, "invalid_limit")
	}
	where, args, err := historyQueryPredicate(q)
	if err != nil {
		return page, err
	}
	if q.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil {
			return page, historyError(400, "invalid_cursor")
		}
		var cur historyCursor
		if e = json.Unmarshal(raw, &cur); e != nil || cur.First.IsZero() || nodeprov.HistoryUUID(cur.Node).Validate() != nil || nodeprov.HistoryUUID(cur.Epoch).Validate() != nil || cur.Entity == "" {
			return page, historyError(400, "invalid_cursor")
		}
		n := len(args)
		args = append(args, cur.First, cur.Node, cur.Epoch, cur.Entity)
		where += " AND (p.first_observed_at,p.node_origin,p.epoch,p.entity_id)<($" + strconv.Itoa(n+1) + ",$" + strconv.Itoa(n+2) + ",$" + strconv.Itoa(n+3) + ",$" + strconv.Itoa(n+4) + ")"
	}
	table := "connection_sessions"
	if flows {
		table = "connection_flows"
	}
	args = append(args, q.Limit+1)
	rows, err := s.db.Query(ctx, `SELECT p.node_origin::text,coalesce(c.node_snapshot,''),b.user_id::text,b.device_id::text,b.user_snapshot,b.device_snapshot,
 p.first_observed_at,p.observation_status,p.payload,p.epoch::text,p.entity_id
 FROM `+table+` p `+historyQueryJoin+where+` ORDER BY p.first_observed_at DESC,p.node_origin DESC,p.epoch DESC,p.entity_id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return page, err
	}
	var cursors []historyCursor
	for rows.Next() {
		var row ConnectionHistoryRow
		var raw []byte
		var epoch, entity string
		if err = rows.Scan(&row.NodeID, &row.NodeName, &row.UserID, &row.DeviceID, &row.UserSnapshot, &row.DeviceSnapshot, &row.FirstObservedAt, &row.ObservationStatus, &raw, &epoch, &entity); err != nil {
			rows.Close()
			return page, err
		}
		if err = json.Unmarshal(raw, &row.Event); err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, row)
		cursors = append(cursors, historyCursor{row.FirstObservedAt, row.NodeID, epoch, entity})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		raw, e := json.Marshal(cursors[q.Limit-1])
		if e != nil {
			return page, e
		}
		next := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &next
	}
	for i := range page.Items {
		row := &page.Items[i]
		row.GapCoverage, err = s.Gaps(ctx, row.NodeID, string(row.Event.CollectionEpoch), row.Event.ProducerID, string(row.Event.ObserverBootID))
		if err != nil {
			return page, err
		}
		if len(row.GapCoverage) > 0 {
			row.Event.Quality.Completeness = "unknown"
			row.Event.Bytes.Final = false
			if row.Event.Lifecycle.EndedAt == nil {
				row.ObservationStatus = "interrupted"
			}
		}
		if !flows {
			childQ := ConnectionHistoryQuery{NodeID: row.NodeID, Epoch: string(row.Event.CollectionEpoch), TunnelID: *row.Event.TunnelID}
			row.ChildTotals, err = s.Aggregate(ctx, childQ)
			if err != nil {
				return page, err
			}
		}
	}
	return page, nil
}

func (s *ConnectionHistoryService) Gaps(ctx context.Context, node, epoch, producer, boot string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	rows, err := s.db.Query(ctx, `SELECT payload FROM connection_log_gaps WHERE node_origin=$1 AND epoch=$2
 AND ($3='' OR payload->'gap'->>'producer_id'=$3) AND ($4='' OR payload->'gap'->>'observer_boot_id'=$4) ORDER BY received_at,event_id`, node, epoch, producer, boot)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}

// Aggregate sums only complete observed flow intervals, separately per byte
// layer. Partial/gapped intervals are counted, never promoted to exact totals.
func (s *ConnectionHistoryService) Aggregate(ctx context.Context, q ConnectionHistoryQuery) ([]ConnectionHistoryLayerTotal, error) {
	out := []ConnectionHistoryLayerTotal{}
	where, args, err := historyQueryPredicate(q)
	if err != nil {
		return out, err
	}
	complete := `p.ended_at IS NOT NULL AND p.started_at IS NOT NULL AND (p.payload->'bytes'->>'final')::boolean
 AND p.payload->'quality'->>'completeness'='complete'
 AND NOT EXISTS(SELECT 1 FROM connection_log_gaps g WHERE g.node_origin=p.node_origin AND g.epoch=p.epoch AND g.payload->'gap'->>'producer_id'=p.producer_id AND g.payload->'gap'->>'observer_boot_id'=p.boot_id::text)`
	rows, err := s.db.Query(ctx, `SELECT p.layer,coalesce(sum(p.upload) FILTER(WHERE `+complete+`),0)::text,coalesce(sum(p.download) FILTER(WHERE `+complete+`),0)::text,
 count(*) FILTER(WHERE `+complete+`),count(*) FILTER(WHERE NOT (`+complete+`)) FROM connection_flows p `+historyQueryJoin+where+` GROUP BY p.layer ORDER BY p.layer`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var total ConnectionHistoryLayerTotal
		if err = rows.Scan(&total.Layer, &total.Upload, &total.Download, &total.CompleteIntervals, &total.PartialIntervals); err != nil {
			return out, err
		}
		total.BoundaryPolicy = "complete_observed_intervals_only"
		out = append(out, total)
	}
	return out, rows.Err()
}
