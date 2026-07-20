package fibermap

import (
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
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// writeTestCert generates a self-signed cert for 127.0.0.1, writes the
// PEM pair into t.TempDir(), and returns the file paths plus a cert
// pool that trusts it — so the test's HTTPS client verifies properly
// instead of using InsecureSkipVerify.
func writeTestCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fibermap-tls-test"},
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

func httpsClient(pool *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
}

// tlsTestEngine builds an engine with one /v1/ping route.
func tlsTestEngine() *Engine[engCtx] {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		return c.SendString("pong-tls")
	})
	return e
}

// Acceptance: WithTLS serves HTTPS on addr; plain HTTP to the same
// port fails at the TLS layer. Covers the graceful (default) Listen
// branch and, in the second subtest, the no-signals branch.
func TestRun_WithTLS_ServesHTTPS(t *testing.T) {
	certFile, keyFile, pool := writeTestCert(t)

	for _, tc := range []struct {
		name string
		opts []RunOption
	}{
		{"graceful branch", nil},
		{"no-signals branch", []RunOption{WithoutSignalHandling()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := tlsTestEngine()
			opts := append([]RunOption{
				WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
				WithTLS(certFile, keyFile),
			}, tc.opts...)
			addr, runErr, stop := runAndWait(t, e, opts...)
			defer stop()

			resp, err := httpsClient(pool).Get("https://" + addr + "/v1/ping")
			if err != nil {
				t.Fatalf("HTTPS GET failed: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || string(body) != "pong-tls" {
				t.Errorf("status=%d body=%q, want 200/pong-tls", resp.StatusCode, string(body))
			}

			// Plain HTTP against the TLS listener must NOT work.
			if plainResp, err := newHTTPClient().Get("http://" + addr + "/v1/ping"); err == nil {
				plainResp.Body.Close()
				t.Errorf("plain HTTP succeeded with status %d, want TLS-layer failure", plainResp.StatusCode)
			}

			// Graceful shutdown works in the TLS branch too.
			stop()
			select {
			case err := <-runErr:
				if err != nil {
					t.Errorf("Run returned %v, want nil after Shutdown", err)
				}
			case <-time.After(2 * time.Second):
				t.Error("Run did not return after Shutdown")
			}
		})
	}
}

// Acceptance: cert without key (or key without cert) is a config
// error — Run must refuse to start, not silently fall back to HTTP.
func TestRun_WithTLS_IncompleteConfig_Errors(t *testing.T) {
	certFile, keyFile, _ := writeTestCert(t)

	for _, tc := range []struct {
		name      string
		cert, key string
	}{
		{"cert only", certFile, ""},
		{"key only", "", keyFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := tlsTestEngine()
			err := e.Run(
				WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
				WithAddr(freeListenAddr(t)),
				WithTLS(tc.cert, tc.key),
			)
			var fe *Error
			if !errors.As(err, &fe) || fe.Code != CodeInvalidTLSConfig {
				t.Errorf("err = %v, want *Error{Code: CodeInvalidTLSConfig}", err)
			}
		})
	}
}
