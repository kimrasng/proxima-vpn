module github.com/proximavpn/proxima-vpn/tools/release-publisher

go 1.25.0

require github.com/proximavpn/proxima-vpn/tools/server-updater v0.0.0

// The verifier lives in the server-updater module of the same checkout; it is
// imported directly so the publisher can never drift from the format the
// updater host actually enforces.
replace github.com/proximavpn/proxima-vpn/tools/server-updater => ../server-updater