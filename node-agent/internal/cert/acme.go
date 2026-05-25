package cert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

const renewBeforeDays = 30

type CertManager struct {
	domain  string
	email   string
	certDir string
	cancel  context.CancelFunc
}

func NewCertManager(domain, email, certDir string) *CertManager {
	return &CertManager{
		domain:  domain,
		email:   email,
		certDir: certDir,
	}
}

// ObtainOrRenew checks if a valid cert exists; if not or expiring soon, obtains a new one.
func (m *CertManager) ObtainOrRenew(ctx context.Context) (certPath, keyPath string, err error) {
	certPath = filepath.Join(m.certDir, m.domain+".crt")
	keyPath = filepath.Join(m.certDir, m.domain+".key")

	if m.certIsValid(certPath) {
		return certPath, keyPath, nil
	}

	if err := os.MkdirAll(m.certDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create cert dir: %w", err)
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate acme key: %w", err)
	}

	user := &acmeUser{email: m.email, key: privateKey}
	legoConfig := lego.NewConfig(user)
	legoConfig.Certificate.KeyType = certcrypto.RSA2048

	legoClient, err := lego.NewClient(legoConfig)
	if err != nil {
		return "", "", fmt.Errorf("create lego client: %w", err)
	}

	if err := legoClient.Challenge.SetHTTP01Provider(http01.NewProviderServer("", "80")); err != nil {
		return "", "", fmt.Errorf("set http01 provider: %w", err)
	}

	reg, err := legoClient.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return "", "", fmt.Errorf("register acme account: %w", err)
	}
	user.registration = reg

	request := certificate.ObtainRequest{
		Domains: []string{m.domain},
		Bundle:  true,
	}

	certificates, err := legoClient.Certificate.Obtain(request)
	if err != nil {
		return "", "", fmt.Errorf("obtain certificate: %w", err)
	}

	if err := os.WriteFile(certPath, certificates.Certificate, 0o644); err != nil {
		return "", "", fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, certificates.PrivateKey, 0o600); err != nil {
		return "", "", fmt.Errorf("write key: %w", err)
	}

	return certPath, keyPath, nil
}

func (m *CertManager) StartAutoRenew(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	go m.renewLoop(ctx)
}

func (m *CertManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *CertManager) renewLoop(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := m.ObtainOrRenew(ctx); err != nil {
				log.Printf("cert auto-renew: %v", err)
			}
		}
	}
}

func (m *CertManager) certIsValid(certPath string) bool {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}

	pair, err := tls.X509KeyPair(data, data)
	if err != nil {
		// Try loading just the cert
		certs, err := x509.ParseCertificates(data)
		if err != nil || len(certs) == 0 {
			return false
		}
		return time.Until(certs[0].NotAfter) > renewBeforeDays*24*time.Hour
	}

	if len(pair.Certificate) == 0 {
		return false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return false
	}
	return time.Until(cert.NotAfter) > renewBeforeDays*24*time.Hour
}

// acmeUser implements registration.User for lego.
type acmeUser struct {
	email        string
	registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }
