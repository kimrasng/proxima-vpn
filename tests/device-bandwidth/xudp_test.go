package bandwidth_test

import (
	"fmt"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestRealXrayVisionXUDPEcho(t *testing.T) {
	binary := requireXray(t)
	a := newAuthority()
	a.add(deviceA, rateA, burst)
	tcp := payloadServer(t)
	s := limiter(t, a, tcp, udpEcho(t), nil)
	private, public := realityKeys(t)
	serverPort := reserveTCPPort(t)
	startXray(t, binary, "xudp-server", generatedServer(t, serverPort, private, tcp, s), serverPort)
	port := reserveTCPPort(t)
	cfg := xrayClientConfig(port, serverPort, deviceA, public)
	outbound := cfg["outbounds"].([]any)[0].(map[string]any)
	outbound["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["flow"] = "xtls-rprx-vision-udp443"
	outbound["mux"] = map[string]any{
		"enabled": false, "xudpConcurrency": 8, "xudpProxyUDP443": "allow",
	}
	startXray(t, binary, "xudp-client", cfg, port)
	result := udpProbe(t, fmt.Sprintf("127.0.0.1:%d", port), "", "", window, rateA, burst+devicebandwidth.MaxChunk)
	recordEvidence(t, "vision_xudp_echo", result)
}
