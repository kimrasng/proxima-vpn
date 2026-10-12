package services

import (
	"context"
	"fmt"
)

// Route apply states shown per chain. A route is only "applied" when every
// node that installs part of it (the Entry for its DNAT rule, the Exit for its
// relay-only admission rule) reports the fingerprint the panel expects now.
const (
	RouteApplyApplied = "applied"
	RouteApplyPending = "pending" // a node still runs an older policy
	RouteApplyFailed  = "failed"  // a node's last reconciliation failed
	RouteApplyUnknown = "unknown" // a node never reported (agent predates reporting)
	RouteApplyNotUsed = "not_applicable"
)

// RouteApplyStatus is the apply state of one chain plus the first node error.
type RouteApplyStatus struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// NodePolicyState compares what a node reported with what it should run.
type NodePolicyState struct {
	State string
	Error string
}

// NodePolicyStates evaluates every node that takes part in a relayed chain,
// computing each expected fingerprint once.
func NodePolicyStates(ctx context.Context, db policyQuerier) (map[string]NodePolicyState, error) {
	rows, err := db.Query(ctx,
		`SELECT n.id::text, COALESCE(n.policy_fingerprint, ''), COALESCE(n.policy_error, '')
		 FROM nodes n
		 WHERE n.status <> 'pending' AND EXISTS (
		   SELECT 1 FROM node_chains c
		   WHERE c.mode = 'l4_dnat' AND c.enabled
		     AND (c.entry_node_id = n.id OR c.exit_node_id = n.id OR EXISTS (
		       SELECT 1 FROM node_group_nodes ngn WHERE ngn.node_group_id = c.relay_pool_id AND ngn.node_id = n.id)))`)
	if err != nil {
		return nil, fmt.Errorf("read node policy reports: %w", err)
	}
	type report struct{ id, fingerprint, failure string }
	var reports []report
	for rows.Next() {
		var r report
		if err := rows.Scan(&r.id, &r.fingerprint, &r.failure); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan node policy report: %w", err)
		}
		reports = append(reports, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read node policy reports: %w", err)
	}

	states := make(map[string]NodePolicyState, len(reports))
	for _, r := range reports {
		expected, ok, err := ExpectedPolicyFingerprint(ctx, db, r.id)
		if err != nil {
			states[r.id] = NodePolicyState{State: RouteApplyFailed, Error: err.Error()}
			continue
		}
		switch {
		case !ok:
			states[r.id] = NodePolicyState{State: RouteApplyUnknown}
		case r.fingerprint == "" && r.failure == "":
			states[r.id] = NodePolicyState{State: RouteApplyUnknown}
		case r.fingerprint == expected && r.failure == "":
			states[r.id] = NodePolicyState{State: RouteApplyApplied}
		case r.failure != "":
			// A failure with the expected fingerprint means a later poll
			// failed while the current policy is still installed; it is in
			// force, but the error is worth showing.
			state := RouteApplyFailed
			if r.fingerprint == expected {
				state = RouteApplyApplied
			}
			states[r.id] = NodePolicyState{State: state, Error: r.failure}
		default:
			states[r.id] = NodePolicyState{State: RouteApplyPending}
		}
	}
	return states, nil
}

// CombineRouteApply folds the states of a chain's participating nodes into
// one: any failure wins, then unknown, then pending; all applied is applied.
func CombineRouteApply(nodes ...NodePolicyState) RouteApplyStatus {
	rank := map[string]int{RouteApplyApplied: 0, RouteApplyPending: 1, RouteApplyUnknown: 2, RouteApplyFailed: 3}
	out := RouteApplyStatus{State: RouteApplyApplied}
	for _, node := range nodes {
		if node.State == "" {
			node.State = RouteApplyUnknown
		}
		if rank[node.State] > rank[out.State] {
			out.State = node.State
		}
		if out.Error == "" && node.Error != "" {
			out.Error = node.Error
		}
	}
	return out
}
