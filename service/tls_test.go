package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// writeTLSTestCert mirrors the fibermap test helper: self-signed cert
// for 127.0.0.1 written into t.TempDir(), plus a trusting cert pool.
func writeTLSTestCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "service-tls-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to add test cert to pool")
	}
	return certFile, keyFile, pool
}

func TestConfigValidate_TLSPair_BothOrNone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cert, key string
		wantErr   bool
	}{
		{"both empty", "", "", false},
		{"both set", "cert.pem", "key.pem", false},
		{"cert only", "cert.pem", "", true},
		{"key only", "", "key.pem", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{}
			cfg.Service.TLSCertFile = tc.cert
			cfg.Service.TLSKeyFile = tc.key
			err := cfg.Validate()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate: unexpected error %v", err)
				}
				return
			}
			var xe *xerrs.Error
			if !errors.As(err, &xe) || xe.Code != CodeTLSConfigIncomplete {
				t.Errorf("err = %v, want *errs.Error{Code: CodeTLSConfigIncomplete}", err)
			}
		})
	}
}

func TestResolveTLS_OptionWinsOverConfig(t *testing.T) {
	cfg := Config{}
	cfg.Service.TLSCertFile = "env-cert.pem"
	cfg.Service.TLSKeyFile = "env-key.pem"

	o := &options{}
	WithTLS("opt-cert.pem", "opt-key.pem")(o)

	svc := &Service[struct{}, struct{}]{cfg: cfg, opts: o}
	cert, key := svc.resolveTLS()
	if cert != "opt-cert.pem" || key != "opt-key.pem" {
		t.Errorf("resolveTLS = (%q, %q), want option pair to win over env", cert, key)
	}
}

func TestResolveTLS_ConfigUsedWithoutOption(t *testing.T) {
	cfg := Config{}
	cfg.Service.TLSCertFile = "env-cert.pem"
	cfg.Service.TLSKeyFile = "env-key.pem"

	svc := &Service[struct{}, struct{}]{cfg: cfg, opts: &options{}}
	cert, key := svc.resolveTLS()
	if cert != "env-cert.pem" || key != "env-key.pem" {
		t.Errorf("resolveTLS = (%q, %q), want env pair", cert, key)
	}
}

func TestResolveTLS_NothingConfigured_Empty(t *testing.T) {
	svc := &Service[struct{}, struct{}]{cfg: Config{}, opts: &options{}}
	cert, key := svc.resolveTLS()
	if cert != "" || key != "" {
		t.Errorf("resolveTLS = (%q, %q), want empty pair (plain HTTP)", cert, key)
	}
}

// Acceptance e2e: service.WithTLS makes svc.Run serve HTTPS; plain
// HTTP to the same port fails; graceful shutdown still works.
func TestServiceRun_WithTLS_ServesHTTPS(t *testing.T) {
	certFile, keyFile, pool := writeTLSTestCert(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cfg := Config{}
	cfg.Service.Addr = addr
	cfg.Service.LogLevel = "error"

	appCh := make(chan *fiber.App, 1)
	ready := make(chan struct{})
	svc, err := NewSimple(context.Background(), cfg,
		WithTLS(certFile, keyFile),
		WithRunOptions(fibermap.WithConfigureApp(func(app *fiber.App) {
			app.Hooks().OnListen(func(fiber.ListenData) error {
				close(ready)
				return nil
			})
			appCh <- app
		})),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.SetContextBuilder(func(c *fiber.Ctx) (struct{}, error) { return struct{}{}, nil })
	if err := svc.Engine.LoadBytes([]byte("groups: []\n")); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- svc.Run() }()

	var app *fiber.App
	select {
	case app = <-appCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Run never built the fiber app")
	}
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not start listening")
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	resp, err := client.Get("https://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("HTTPS GET /healthz failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("healthz over TLS: status=%d body=%q", resp.StatusCode, string(body))
	}

	plain := &http.Client{Timeout: 2 * time.Second}
	if plainResp, err := plain.Get("http://" + addr + "/healthz"); err == nil {
		plainResp.Body.Close()
		t.Errorf("plain HTTP succeeded (%d), want TLS-layer failure", plainResp.StatusCode)
	}

	_ = app.Shutdown()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run returned %v, want nil after Shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Run did not return after Shutdown")
	}
}

// Incomplete pair via option must fail Run with fibermap's typed
// error, not start plain HTTP.
func TestServiceRun_WithTLS_IncompletePair_FailsRun(t *testing.T) {
	certFile, _, _ := writeTLSTestCert(t)

	cfg := Config{}
	cfg.Service.Addr = "127.0.0.1:0"
	cfg.Service.LogLevel = "error"

	svc, err := NewSimple(context.Background(), cfg, WithTLS(certFile, ""))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	svc.SetContextBuilder(func(c *fiber.Ctx) (struct{}, error) { return struct{}{}, nil })
	if err := svc.Engine.LoadBytes([]byte("groups: []\n")); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	err = svc.Run()
	if err == nil || !strings.Contains(err.Error(), "invalid_tls_config") {
		t.Errorf("Run = %v, want invalid_tls_config error", err)
	}
}
