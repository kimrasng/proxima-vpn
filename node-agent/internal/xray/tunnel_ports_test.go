package xray

import (
	"reflect"
	"testing"
)

func TestTunnelPortsOnlyIncludesVLESSListeners(t *testing.T) {
	got, err := tunnelPorts([]byte(`{"inbounds":[{"protocol":"vless","port":8443},{"protocol":"vless","port":443},{"protocol":"vless","port":8443},{"protocol":"socks","port":10085},{"protocol":"vmess","port":1234}]}`))
	if err != nil || !reflect.DeepEqual(got, []int{443, 8443}) {
		t.Fatalf("ports=%v err=%v", got, err)
	}
	for _, data := range []string{`invalid`, `{"inbounds":[{"protocol":"vless","port":0}]}`} {
		if _, err := tunnelPorts([]byte(data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}
