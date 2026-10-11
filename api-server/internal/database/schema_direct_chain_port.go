package database

// A direct chain is the exit itself, so its exit_port must be the port the
// exit's Xray actually listens on. That is the oldest enabled inbound when the
// node has one, and otherwise nodes.port, which is exactly what
// XrayConfigService.GenerateConfig serves (legacy inbounds at nodePort when no
// enabled inbound exists). Copying nodes.port at registration let the two drift
// as soon as an operator created an inbound on another port (8443 vs 20100).
//
// The value is enforced in the database rather than in each writer: direct
// chains are written by node registration, the migration backfill and node
// group edits, and the listening port moves with inbound create, update,
// toggle and delete. Triggers cover all of them atomically.
var directChainPortMigrations = []string{
	// NULL when the node does not exist, including mid-cascade while it is being
	// deleted; callers treat that as "nothing to sync".
	`CREATE OR REPLACE FUNCTION node_listening_port(node UUID) RETURNS INT
	 LANGUAGE sql STABLE AS $$
		SELECT COALESCE(
			(SELECT i.port FROM inbounds i
			 WHERE i.node_id = n.id AND i.enabled
			 ORDER BY i.created_at, i.id LIMIT 1),
			n.port)
		FROM nodes n WHERE n.id = node
	 $$`,

	// Direct chains always carry the listening port, whatever the writer sent.
	`CREATE OR REPLACE FUNCTION node_chains_direct_exit_port() RETURNS trigger
	 LANGUAGE plpgsql AS $$
	 BEGIN
		IF NEW.relay_pool_id IS NULL AND NEW.entry_node_id IS NULL THEN
			NEW.exit_port := COALESCE(node_listening_port(NEW.exit_node_id), NEW.exit_port);
		END IF;
		RETURN NEW;
	 END $$`,
	`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'node_chains'::regclass AND tgname = 'node_chains_direct_exit_port') THEN
			CREATE TRIGGER node_chains_direct_exit_port BEFORE INSERT OR UPDATE ON node_chains
			FOR EACH ROW EXECUTE FUNCTION node_chains_direct_exit_port();
		END IF;
	 END $$`,

	// Re-sync a node's direct chain when what it listens on changes. NULL means
	// the node is gone (deleting it cascades its inbounds and chains in the same
	// statement), so touching its chains would conflict with that delete.
	//
	// nodes.port only changes at registration, which rewrites the direct chain
	// itself under its own row lock (node_agent.go Register); the BEFORE trigger
	// above corrects that write, so no trigger on nodes is needed - and adding one
	// would take the chain lock earlier than Register's SELECT ... FOR UPDATE.
	`CREATE OR REPLACE FUNCTION sync_direct_chain_exit_port(node UUID) RETURNS void
	 LANGUAGE plpgsql AS $$
	 DECLARE
		listening INT := node_listening_port(node);
	 BEGIN
		IF listening IS NULL THEN
			RETURN;
		END IF;
		UPDATE node_chains SET exit_port = listening
		 WHERE exit_node_id = node AND relay_pool_id IS NULL AND entry_node_id IS NULL
		   AND exit_port IS DISTINCT FROM listening;
	 END $$`,
	`CREATE OR REPLACE FUNCTION inbounds_sync_direct_chain() RETURNS trigger
	 LANGUAGE plpgsql AS $$
	 BEGIN
		IF TG_OP IN ('UPDATE', 'DELETE') THEN
			PERFORM sync_direct_chain_exit_port(OLD.node_id);
		END IF;
		IF TG_OP IN ('INSERT', 'UPDATE') AND (TG_OP = 'INSERT' OR NEW.node_id IS DISTINCT FROM OLD.node_id) THEN
			PERFORM sync_direct_chain_exit_port(NEW.node_id);
		END IF;
		RETURN NULL;
	 END $$`,
	`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'inbounds'::regclass AND tgname = 'inbounds_sync_direct_chain') THEN
			CREATE TRIGGER inbounds_sync_direct_chain AFTER INSERT OR DELETE OR UPDATE OF node_id, port, enabled ON inbounds
			FOR EACH ROW EXECUTE FUNCTION inbounds_sync_direct_chain();
		END IF;
	 END $$`,

	// Repair chains written before the triggers existed.
	`UPDATE node_chains c SET exit_port = node_listening_port(c.exit_node_id)
	 WHERE c.relay_pool_id IS NULL AND c.entry_node_id IS NULL
	   AND node_listening_port(c.exit_node_id) IS NOT NULL
	   AND c.exit_port IS DISTINCT FROM node_listening_port(c.exit_node_id)`,
}
