package updater

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The real CLI worker uses its normal HTTPS URLs and proxy/certificate support.
// This proxy terminates TLS locally and serves only private fixtures. It never
// dials an upstream host; weather and all other requests get a fixed 503.
func fixtureReleaseProxy(t *testing.T, root string, source GitHubSource, pin Pin) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Private updater fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"github.com", "api.github.com", "raw.githubusercontent.com", "api.open-meteo.com", "air-quality-api.open-meteo.com", "api.weather.gov"}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certificate, e := tls.X509KeyPair(ca, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if e != nil {
		t.Fatal(e)
	}
	caPath := filepath.Join(root, "fixture-ca.pem")
	if e = os.WriteFile(caPath, ca, 0600); e != nil {
		t.Fatal(e)
	}
	config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	var connections sync.Map
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "fixture only", http.StatusServiceUnavailable)
			return
		}
		raw, buffer, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		connections.Store(raw, true)
		defer connections.Delete(raw)
		defer raw.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if e = buffer.Flush(); e != nil {
			return
		}
		conn := tls.Server(raw, config)
		if e = conn.Handshake(); e != nil {
			return
		}
		reader := bufio.NewReader(conn)
		for {
			request, e := http.ReadRequest(reader)
			if e != nil {
				return
			}
			request.URL.Scheme = "https"
			request.URL.Host = request.Host
			request.RequestURI = ""
			response := &http.Response{StatusCode: 503, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("offline fixture")), ContentLength: int64(len("offline fixture"))}
			var body []byte
			if request.Host == "api.github.com" && request.URL.Path == "/repos/joega/a-weather-app/releases/latest" {
				body, _ = json.Marshal(map[string]any{"tag_name": pin.Tag, "draft": false, "prerelease": false})
			} else if request.Host == "raw.githubusercontent.com" && request.URL.Path == "/joega/a-weather-app/main/packaging/release-lock.json" {
				body, _ = json.Marshal(pin)
			} else if request.Host == "github.com" && strings.HasPrefix(request.URL.Path, "/joega/a-weather-app/releases/download/"+pin.Tag+"/") {
				if asset, e := source.Client.Do(request); e == nil {
					response = asset
				}
			}
			if body != nil {
				response.StatusCode = 200
				response.Body = io.NopCloser(strings.NewReader(string(body)))
				response.ContentLength = int64(len(body))
			}
			response.ProtoMajor, response.ProtoMinor = 1, 1
			e = response.Write(conn)
			response.Body.Close()
			request.Body.Close()
			if e != nil {
				return
			}
		}
	}))
	t.Cleanup(func() {
		connections.Range(func(key, value any) bool { _ = key.(net.Conn).Close(); return true })
		proxy.Close()
	})
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("SSL_CERT_FILE", caPath)
	t.Setenv("SSL_CERT_DIR", filepath.Join(root, "no-other-certificates"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
}
