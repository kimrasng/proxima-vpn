package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// policyQuerier is the read surface shared by a pool and a transaction.
type policyQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RelayRulesForNode returns the forwarding rules a node should install as an
// Entry: every enabled l4_dnat chain it is the explicit entry of, or whose
// legacy relay pool it belongs to. The agent endpoint and the panel's
// expected-policy fingerprint both use this one query, so the panel can only
// call a route applied when the node runs exactly what this returns.
func RelayRulesForNode(ctx context.Context, db policyQuerier, nodeID string) ([]nodeprov.RelayRule, error) {
	rows, err := db.Query(ctx,
		`SELECT c.entry_port, c.transport, host(e.ip), c.exit_port
		 FROM node_chains c
		 JOIN nodes relay_node ON relay_node.id = $1
		 JOIN nodes e ON e.id = c.exit_node_id
		 WHERE relay_node.role IN ('relay', 'both')
		   AND (c.entry_node_id = relay_node.id OR (c.entry_node_id IS NULL AND EXISTS (
		     SELECT 1 FROM node_group_nodes ngn
		     WHERE ngn.node_group_id = c.relay_pool_id AND ngn.node_id = relay_node.id
		   )))
		   AND c.enabled = true
		   AND c.mode = 'l4_dnat'
		 ORDER BY c.entry_port, c.transport`,
		nodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch relay rules: %w", err)
	}
	defer rows.Close()

	rules := make([]nodeprov.RelayRule, 0)
	for rows.Next() {
		var r nodeprov.RelayRule
		if err := rows.Scan(&r.EntryPort, &r.Transport, &r.ExitIP, &r.ExitPort); err != nil {
			return nil, fmt.Errorf("scan relay rule: %w", err)
		}
		// A malformed row would otherwise reach nftables as a broken rule and
		// take the whole ruleset down with it, dropping every other chain.
		if err := r.Validate(); err != nil {
			return nil, &InvalidPolicyRuleError{Kind: "relay", Cause: err}
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read relay rules: %w", err)
	}
	return rules, nil
}

// ExitRulesForNode returns the admission rules a node should install as an
// Exit: each relayed chain's exit port is open only to that chain's Entry
// addresses.
func ExitRulesForNode(ctx context.Context, db policyQuerier, nodeID string) ([]nodeprov.ExitRule, error) {
	rows, err := db.Query(ctx,
		`SELECT c.exit_port, c.transport,
		        ARRAY(
		          SELECT host(relay.ip)
		          FROM (
		            SELECT DISTINCT n.ip
		            FROM nodes n
		            WHERE (n.id = c.entry_node_id OR (c.entry_node_id IS NULL AND EXISTS (
		              SELECT 1 FROM node_group_nodes ngn
		              WHERE ngn.node_group_id = c.relay_pool_id AND ngn.node_id = n.id
		            )))
		              AND n.role IN ('relay', 'both')
		              AND family(n.ip) = 4
		            ORDER BY n.ip
		          ) relay
		        )
		 FROM node_chains c
		 WHERE c.exit_node_id = $1
		   AND (c.entry_node_id IS NOT NULL OR c.relay_pool_id IS NOT NULL)
		   AND c.enabled = true
		   AND c.mode = 'l4_dnat'
		 ORDER BY c.exit_port, c.transport, c.id`,
		nodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch exit rules: %w", err)
	}
	defer rows.Close()

	rules := make([]nodeprov.ExitRule, 0)
	for rows.Next() {
		var rule nodeprov.ExitRule
		if err := rows.Scan(&rule.ExitPort, &rule.Transport, &rule.RelayIPs); err != nil {
			return nil, fmt.Errorf("scan exit rule: %w", err)
		}
		if err := rule.Validate(); err != nil {
			return nil, &InvalidPolicyRuleError{Kind: "exit", Cause: err}
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read exit rules: %w", err)
	}
	nodeprov.SortExitRules(rules)
	return rules, nil
}

// ExpectedPolicyFingerprint is the nodeprov.PolicyFingerprint of the policy
// nodeID should be running now, computed from the same queries its agent polls.
// ok is false for a node whose role is unknown.
func ExpectedPolicyFingerprint(ctx context.Context, db policyQuerier, nodeID string) (fingerprint string, ok bool, err error) {
	var roleText string
	if err := db.QueryRow(ctx, `SELECT role FROM nodes WHERE id = $1`, nodeID).Scan(&roleText); err != nil {
		return "", false, fmt.Errorf("read node role: %w", err)
	}
	role := nodeprov.Role(roleText)
	if !role.Valid() {
		return "", false, nil
	}
	var relay []nodeprov.RelayRule
	var exit []nodeprov.ExitRule
	if role.Forwards() {
		if relay, err = RelayRulesForNode(ctx, db, nodeID); err != nil {
			return "", false, err
		}
	}
	if role.Exits() {
		if exit, err = ExitRulesForNode(ctx, db, nodeID); err != nil {
			return "", false, err
		}
	}
	return nodeprov.PolicyFingerprint(role, relay, exit), true, nil
}

// InvalidPolicyRuleError reports a stored chain that cannot become a valid
// nftables rule; the agent endpoints surface it as a 500 with its detail.
type InvalidPolicyRuleError struct {
	Kind  string
	Cause error
}

func (e *InvalidPolicyRuleError) Error() string {
	return fmt.Sprintf("invalid %s rule: %v", e.Kind, e.Cause)
}

func (e *InvalidPolicyRuleError) Unwrap() error { return e.Cause }
