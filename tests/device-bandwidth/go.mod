module github.com/proximavpn/proxima-vpn/node-agent/tests/device-bandwidth

go 1.25.0

require (
	github.com/proximavpn/proxima-vpn/node-agent v0.0.0
	github.com/proximavpn/proxima-vpn/pkg v0.0.0
)

replace github.com/proximavpn/proxima-vpn/node-agent => ../../node-agent
replace github.com/proximavpn/proxima-vpn/pkg => ../../pkg

// Kept outside go.work deliberately: the harness never starts the application.
