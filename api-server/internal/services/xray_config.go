package services

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type XrayConfigService struct {
	db *pgxpool.Pool
}

func NewXrayConfigService(db *pgxpool.Pool) *XrayConfigService {
	return &XrayConfigService{db: db}
}

type xrayClient struct {
	ID    string `json:"id"`
	Flow  string `json:"flow,omitempty"`
	Email string `json:"email"`
	Level int    `json:"level"`
}

type xrayTrojanClient struct {
	Password string `json:"password"`
	Email    string `json:"email"`
	Level    int    `json:"level"`
}

type xrayRealitySettings struct {
	Dest        string   `json:"dest"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIds    []string `json:"shortIds"`
}

type xrayTLSCertificate struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type xrayTLSSettings struct {
	Certificates []xrayTLSCertificate `json:"certificates"`
}

type xrayWSSettings struct {
	Path string `json:"path"`
}

type xrayStreamSettings struct {
	Network         string               `json:"network"`
	Security        string               `json:"security"`
	RealitySettings *xrayRealitySettings `json:"realitySettings,omitempty"`
	TLSSettings     *xrayTLSSettings     `json:"tlsSettings,omitempty"`
	WSSettings      *xrayWSSettings      `json:"wsSettings,omitempty"`
}

type xraySniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
}

type xrayInboundSettings struct {
	Clients    []xrayClient `json:"clients"`
	Decryption string       `json:"decryption"`
}

type xrayTrojanInboundSettings struct {
	Clients []xrayTrojanClient `json:"clients"`
}

type xrayShadowsocksSettings struct {
	Method   string `json:"method"`
	Password string `json:"password"`
	Network  string `json:"network"`
}

type xrayVmessInboundSettings struct {
	Clients []xrayClient `json:"clients"`
}

type xrayDokodemoSettings struct {
	Address string `json:"address"`
}

type xrayInbound struct {
	Listen         string              `json:"listen,omitempty"`
	Port           int                 `json:"port"`
	Protocol       string              `json:"protocol"`
	Tag            string              `json:"tag"`
	Settings       json.RawMessage     `json:"settings"`
	StreamSettings *xrayStreamSettings `json:"streamSettings,omitempty"`
	Sniffing       *xraySniffing       `json:"sniffing,omitempty"`
}

type xrayPolicyLevel struct {
	StatsUserUplink   bool `json:"statsUserUplink"`
	StatsUserDownlink bool `json:"statsUserDownlink"`
}

type xraySystemPolicy struct {
	StatsInboundUplink   bool `json:"statsInboundUplink"`
	StatsInboundDownlink bool `json:"statsInboundDownlink"`
}

type xrayPolicy struct {
	Levels map[string]xrayPolicyLevel `json:"levels"`
	System xraySystemPolicy           `json:"system"`
}

type xrayAPI struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

type xrayOutbound struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

type xrayRoutingRule struct {
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
}

type xrayRouting struct {
	Rules []xrayRoutingRule `json:"rules"`
}

type xrayLog struct {
	LogLevel string `json:"loglevel"`
}

type xrayConfig struct {
	Log       xrayLog        `json:"log"`
	Stats     struct{}       `json:"stats"`
	API       xrayAPI        `json:"api"`
	Policy    xrayPolicy     `json:"policy"`
	Inbounds  []xrayInbound  `json:"inbounds"`
	Outbounds []xrayOutbound `json:"outbounds"`
	Routing   xrayRouting    `json:"routing"`
}

type inboundRow struct {
	ID       string
	Protocol string
	Port     int
	Tag      string
	Settings json.RawMessage
	Enabled  bool
}

// GenerateConfig builds a full Xray JSON config for the given node.
func (s *XrayConfigService) GenerateConfig(ctx context.Context, nodeID string) ([]byte, error) {
	var realityPrivateKey, realityShortID string
	var tlsCertFile, tlsKeyFile *string
	var ssPassword *string
	var nodePort int
	err := s.db.QueryRow(ctx,
		`SELECT port, reality_private_key, reality_short_id, tls_cert_file, tls_key_file, ss_password FROM nodes WHERE id = $1`,
		nodeID,
	).Scan(&nodePort, &realityPrivateKey, &realityShortID, &tlsCertFile, &tlsKeyFile, &ssPassword)
	if err != nil {
		return nil, fmt.Errorf("fetch node details: %w", err)
	}

	ibRows, err := s.db.Query(ctx,
		`SELECT id, protocol, port, tag, settings, enabled FROM inbounds WHERE node_id = $1 AND enabled = true ORDER BY created_at`,
		nodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch inbounds: %w", err)
	}
	defer ibRows.Close()

	var dbInbounds []inboundRow
	for ibRows.Next() {
		var ib inboundRow
		if err := ibRows.Scan(&ib.ID, &ib.Protocol, &ib.Port, &ib.Tag, &ib.Settings, &ib.Enabled); err != nil {
			continue
		}
		dbInbounds = append(dbInbounds, ib)
	}

	rows, err := s.db.Query(ctx,
		`SELECT d.xray_uuid, p.speed_limit
		 FROM devices d
		 JOIN users u ON d.user_id = u.id
		 JOIN plans p ON u.plan_id = p.id
		 JOIN node_groups ng ON p.node_group_id = ng.id
		 JOIN node_group_nodes ngn ON ng.id = ngn.node_group_id
		 WHERE ngn.node_id = $1
		   AND u.is_active = true
		   AND u.status = 'active'
		   AND (u.plan_expires_at IS NULL OR u.plan_expires_at > NOW())
		   AND (p.traffic_limit IS NULL OR u.traffic_used < p.traffic_limit)`,
		nodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch clients: %w", err)
	}
	defer rows.Close()

	var vlessClients []xrayClient
	var vmessClients []xrayClient
	var trojanClients []xrayTrojanClient
	policyLevels := map[string]xrayPolicyLevel{
		"0": {StatsUserUplink: true, StatsUserDownlink: true},
	}
	levelIdx := 0

	for rows.Next() {
		var uuid string
		var speedLimit *int64

		if err := rows.Scan(&uuid, &speedLimit); err != nil {
			continue
		}

		level := 0
		if speedLimit != nil && *speedLimit > 0 {
			levelIdx++
			level = levelIdx
			policyLevels[fmt.Sprintf("%d", level)] = xrayPolicyLevel{
				StatsUserUplink:   true,
				StatsUserDownlink: true,
			}
		}

		vlessClients = append(vlessClients, xrayClient{
			ID:    uuid,
			Flow:  "xtls-rprx-vision",
			Email: uuid + "@proxima",
			Level: level,
		})

		vmessClients = append(vmessClients, xrayClient{
			ID:    uuid,
			Email: uuid + "@proxima-vmess",
			Level: level,
		})

		trojanClients = append(trojanClients, xrayTrojanClient{
			Password: uuid,
			Email:    uuid + "@proxima-trojan",
			Level:    level,
		})
	}

	if vlessClients == nil {
		vlessClients = []xrayClient{}
	}
	if vmessClients == nil {
		vmessClients = []xrayClient{}
	}
	if trojanClients == nil {
		trojanClients = []xrayTrojanClient{}
	}

	apiSettings, _ := json.Marshal(xrayDokodemoSettings{
		Address: "127.0.0.1",
	})
	inbounds := []xrayInbound{
		{
			Listen:   "127.0.0.1",
			Port:     10085,
			Protocol: "dokodemo-door",
			Tag:      "api",
			Settings: apiSettings,
		},
	}

	if len(dbInbounds) > 0 {
		for _, ib := range dbInbounds {
			xrayIb, err := s.buildInbound(ib, vlessClients, vmessClients, trojanClients, realityPrivateKey, realityShortID, tlsCertFile, tlsKeyFile)
			if err != nil {
				continue
			}
			inbounds = append(inbounds, *xrayIb)
		}
	} else {
		inbounds = append(inbounds, s.buildLegacyInbounds(nodePort, vlessClients, vmessClients, trojanClients, realityPrivateKey, realityShortID, tlsCertFile, tlsKeyFile, ssPassword)...)
	}

	cfg := xrayConfig{
		Log:   xrayLog{LogLevel: "warning"},
		Stats: struct{}{},
		API: xrayAPI{
			Tag:      "api",
			Services: []string{"StatsService", "HandlerService"},
		},
		Policy: xrayPolicy{
			Levels: policyLevels,
			System: xraySystemPolicy{
				StatsInboundUplink:   true,
				StatsInboundDownlink: true,
			},
		},
		Inbounds: inbounds,
		Outbounds: []xrayOutbound{
			{Protocol: "freedom", Tag: "direct"},
			{Protocol: "blackhole", Tag: "block"},
		},
		Routing: xrayRouting{
			Rules: []xrayRoutingRule{
				{InboundTag: []string{"api"}, OutboundTag: "api"},
			},
		},
	}

	return json.MarshalIndent(cfg, "", "  ")
}

func (s *XrayConfigService) buildInbound(
	ib inboundRow,
	vlessClients []xrayClient,
	vmessClients []xrayClient,
	trojanClients []xrayTrojanClient,
	realityPrivateKey, realityShortID string,
	tlsCertFile, tlsKeyFile *string,
) (*xrayInbound, error) {
	switch ib.Protocol {
	case "vless_reality":
		return s.buildVlessReality(ib, vlessClients, realityPrivateKey, realityShortID)
	case "vmess_ws":
		return s.buildVmessWS(ib, vmessClients, tlsCertFile, tlsKeyFile)
	case "trojan_tls":
		return s.buildTrojanTLS(ib, trojanClients, tlsCertFile, tlsKeyFile)
	case "shadowsocks":
		return s.buildShadowsocks(ib)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", ib.Protocol)
	}
}

func (s *XrayConfigService) buildVlessReality(ib inboundRow, clients []xrayClient, privateKey, shortID string) (*xrayInbound, error) {
	// Parse settings for dest and server_names.
	var settings struct {
		Dest        string   `json:"dest"`
		ServerNames []string `json:"server_names"`
	}
	if err := json.Unmarshal(ib.Settings, &settings); err != nil {
		settings.Dest = "www.microsoft.com:443"
		settings.ServerNames = []string{"www.microsoft.com"}
	}
	if settings.Dest == "" {
		settings.Dest = "www.microsoft.com:443"
	}
	if len(settings.ServerNames) == 0 {
		settings.ServerNames = []string{"www.microsoft.com"}
	}

	inboundSettings, _ := json.Marshal(xrayInboundSettings{
		Clients:    clients,
		Decryption: "none",
	})

	return &xrayInbound{
		Port:     ib.Port,
		Protocol: "vless",
		Tag:      ib.Tag,
		Settings: inboundSettings,
		StreamSettings: &xrayStreamSettings{
			Network:  "tcp",
			Security: "reality",
			RealitySettings: &xrayRealitySettings{
				Dest:        settings.Dest,
				ServerNames: settings.ServerNames,
				PrivateKey:  privateKey,
				ShortIds:    []string{shortID},
			},
		},
		Sniffing: &xraySniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls"},
		},
	}, nil
}

func (s *XrayConfigService) buildVmessWS(ib inboundRow, clients []xrayClient, tlsCertFile, tlsKeyFile *string) (*xrayInbound, error) {
	if tlsCertFile == nil || tlsKeyFile == nil || *tlsCertFile == "" || *tlsKeyFile == "" {
		return nil, fmt.Errorf("vmess_ws requires TLS certificates")
	}

	var settings struct {
		WSPath string `json:"ws_path"`
	}
	if err := json.Unmarshal(ib.Settings, &settings); err != nil {
		settings.WSPath = "/vmess"
	}
	if settings.WSPath == "" {
		settings.WSPath = "/vmess"
	}

	vmessSettings, _ := json.Marshal(xrayVmessInboundSettings{
		Clients: clients,
	})

	return &xrayInbound{
		Port:     ib.Port,
		Protocol: "vmess",
		Tag:      ib.Tag,
		Settings: vmessSettings,
		StreamSettings: &xrayStreamSettings{
			Network:  "ws",
			Security: "tls",
			TLSSettings: &xrayTLSSettings{
				Certificates: []xrayTLSCertificate{
					{CertificateFile: *tlsCertFile, KeyFile: *tlsKeyFile},
				},
			},
			WSSettings: &xrayWSSettings{
				Path: settings.WSPath,
			},
		},
		Sniffing: &xraySniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls"},
		},
	}, nil
}

func (s *XrayConfigService) buildTrojanTLS(ib inboundRow, clients []xrayTrojanClient, tlsCertFile, tlsKeyFile *string) (*xrayInbound, error) {
	if tlsCertFile == nil || tlsKeyFile == nil || *tlsCertFile == "" || *tlsKeyFile == "" {
		return nil, fmt.Errorf("trojan_tls requires TLS certificates")
	}

	trojanSettings, _ := json.Marshal(xrayTrojanInboundSettings{
		Clients: clients,
	})

	return &xrayInbound{
		Port:     ib.Port,
		Protocol: "trojan",
		Tag:      ib.Tag,
		Settings: trojanSettings,
		StreamSettings: &xrayStreamSettings{
			Network:  "tcp",
			Security: "tls",
			TLSSettings: &xrayTLSSettings{
				Certificates: []xrayTLSCertificate{
					{CertificateFile: *tlsCertFile, KeyFile: *tlsKeyFile},
				},
			},
		},
		Sniffing: &xraySniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls"},
		},
	}, nil
}

func (s *XrayConfigService) buildShadowsocks(ib inboundRow) (*xrayInbound, error) {
	var settings struct {
		Method   string `json:"method"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ib.Settings, &settings); err != nil {
		return nil, fmt.Errorf("invalid shadowsocks settings: %w", err)
	}
	if settings.Method == "" {
		settings.Method = "2022-blake3-aes-128-gcm"
	}
	if settings.Password == "" {
		return nil, fmt.Errorf("shadowsocks requires a password in settings")
	}

	ssSettings, _ := json.Marshal(xrayShadowsocksSettings{
		Method:   settings.Method,
		Password: settings.Password,
		Network:  "tcp,udp",
	})

	return &xrayInbound{
		Port:     ib.Port,
		Protocol: "shadowsocks",
		Tag:      ib.Tag,
		Settings: ssSettings,
		Sniffing: &xraySniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls"},
		},
	}, nil
}

func (s *XrayConfigService) buildLegacyInbounds(
	port int,
	vlessClients []xrayClient,
	vmessClients []xrayClient,
	trojanClients []xrayTrojanClient,
	realityPrivateKey, realityShortID string,
	tlsCertFile, tlsKeyFile *string,
	ssPassword *string,
) []xrayInbound {
	vlessSettings, _ := json.Marshal(xrayInboundSettings{
		Clients:    vlessClients,
		Decryption: "none",
	})

	result := []xrayInbound{
		{
			Port:     port,
			Protocol: "vless",
			Tag:      "vless-reality",
			Settings: vlessSettings,
			StreamSettings: &xrayStreamSettings{
				Network:  "tcp",
				Security: "reality",
				RealitySettings: &xrayRealitySettings{
					Dest:        "www.microsoft.com:443",
					ServerNames: []string{"www.microsoft.com"},
					PrivateKey:  realityPrivateKey,
					ShortIds:    []string{realityShortID},
				},
			},
			Sniffing: &xraySniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls"},
			},
		},
	}

	hasTLS := tlsCertFile != nil && tlsKeyFile != nil && *tlsCertFile != "" && *tlsKeyFile != ""
	if hasTLS {
		tlsCerts := []xrayTLSCertificate{
			{CertificateFile: *tlsCertFile, KeyFile: *tlsKeyFile},
		}

		vmessSettings, _ := json.Marshal(xrayVmessInboundSettings{
			Clients: vmessClients,
		})
		result = append(result, xrayInbound{
			Port:     8443,
			Protocol: "vmess",
			Tag:      "vmess-ws-tls",
			Settings: vmessSettings,
			StreamSettings: &xrayStreamSettings{
				Network:  "ws",
				Security: "tls",
				TLSSettings: &xrayTLSSettings{
					Certificates: tlsCerts,
				},
				WSSettings: &xrayWSSettings{
					Path: "/vmess",
				},
			},
			Sniffing: &xraySniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls"},
			},
		})

		trojanSettings, _ := json.Marshal(xrayTrojanInboundSettings{
			Clients: trojanClients,
		})
		result = append(result, xrayInbound{
			Port:     2083,
			Protocol: "trojan",
			Tag:      "trojan-tls",
			Settings: trojanSettings,
			StreamSettings: &xrayStreamSettings{
				Network:  "tcp",
				Security: "tls",
				TLSSettings: &xrayTLSSettings{
					Certificates: tlsCerts,
				},
			},
			Sniffing: &xraySniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls"},
			},
		})
	}

	if ssPassword != nil && *ssPassword != "" {
		ssSettings, _ := json.Marshal(xrayShadowsocksSettings{
			Method:   "2022-blake3-aes-128-gcm",
			Password: *ssPassword,
			Network:  "tcp,udp",
		})
		result = append(result, xrayInbound{
			Port:     8388,
			Protocol: "shadowsocks",
			Tag:      "ss",
			Settings: ssSettings,
			Sniffing: &xraySniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls"},
			},
		})
	}

	return result
}
