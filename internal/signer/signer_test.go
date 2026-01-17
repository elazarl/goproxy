package signer_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/internal/signer"
)

func orFatal(t *testing.T, msg string, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(msg, err)
	}
}

type ConstantHanlder string

func (h ConstantHanlder) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, string(h))
}

func getBrowser(args []string) string {
	for i, arg := range args {
		if arg == "-browser" && i+1 < len(arg) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "-browser=") {
			return arg[len("-browser="):]
		}
	}
	return ""
}

func testSignerX509(t *testing.T, ca tls.Certificate) {
	t.Helper()
	cert, err := signer.SignHost(ca, []string{"example.com", "1.1.1.1", "localhost"})
	orFatal(t, "singHost", err)
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	orFatal(t, "ParseCertificate", err)
	certpool := x509.NewCertPool()
	certpool.AddCert(ca.Leaf)
	orFatal(t, "VerifyHostname", cert.Leaf.VerifyHostname("example.com"))
	orFatal(t, "CheckSignatureFrom", cert.Leaf.CheckSignatureFrom(ca.Leaf))
	_, err = cert.Leaf.Verify(x509.VerifyOptions{
		DNSName: "example.com",
		Roots:   certpool,
	})
	orFatal(t, "Verify", err)
}

func testSignerTLS(t *testing.T, ca tls.Certificate) {
	t.Helper()
	cert, err := signer.SignHost(ca, []string{"example.com", "1.1.1.1", "localhost"})
	orFatal(t, "singHost", err)
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	orFatal(t, "ParseCertificate", err)
	expected := "key verifies with Go"
	server := httptest.NewUnstartedServer(ConstantHanlder(expected))
	defer server.Close()
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{*cert, ca},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	certpool := x509.NewCertPool()
	certpool.AddCert(ca.Leaf)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: certpool},
	}
	asLocalhost := strings.ReplaceAll(server.URL, "127.0.0.1", "localhost")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, asLocalhost, nil)
	orFatal(t, "NewRequest", err)
	resp, err := tr.RoundTrip(req)
	orFatal(t, "RoundTrip", err)
	txt, err := io.ReadAll(resp.Body)
	orFatal(t, "io.ReadAll", err)
	if string(txt) != expected {
		t.Errorf("Expected '%s' got '%s'", expected, string(txt))
	}
	browser := getBrowser(os.Args)
	if browser != "" {
		ctx := context.Background()
		_ = exec.CommandContext(ctx, browser, asLocalhost).Run()
		time.Sleep(10 * time.Second)
	}
}

func TestSignerRsaTls(t *testing.T) {
	testSignerTLS(t, goproxy.GoproxyCa)
}

func TestSignerRsaX509(t *testing.T) {
	testSignerX509(t, goproxy.GoproxyCa)
}

func TestSignerEcdsaTls(t *testing.T) {
	testSignerTLS(t, EcdsaCa)
}

func TestSignerEcdsaX509(t *testing.T) {
	testSignerX509(t, EcdsaCa)
}

func BenchmarkSignRsa(b *testing.B) {
	var cert *tls.Certificate
	var err error
	for n := 0; n < b.N; n++ {
		cert, err = signer.SignHost(goproxy.GoproxyCa, []string{"example.com", "1.1.1.1", "localhost"})
	}
	_ = cert
	_ = err
}

func BenchmarkSignEcdsa(b *testing.B) {
	var cert *tls.Certificate
	var err error
	for n := 0; n < b.N; n++ {
		cert, err = signer.SignHost(EcdsaCa, []string{"example.com", "1.1.1.1", "localhost"})
	}
	_ = cert
	_ = err
}

//
// Eliptic Curve certificate and key for testing
//

var EcdsaCaCert = []byte(`-----BEGIN CERTIFICATE-----
MIICGDCCAb8CFEkSgqYhlT0+Yyr9anQNJgtclTL0MAoGCCqGSM49BAMDMIGOMQsw
CQYDVQQGEwJJTDEPMA0GA1UECAwGQ2VudGVyMQwwCgYDVQQHDANMb2QxEDAOBgNV
BAoMB0dvUHJveHkxEDAOBgNVBAsMB0dvUHJveHkxGjAYBgNVBAMMEWdvcHJveHku
Z2l0aHViLmlvMSAwHgYJKoZIhvcNAQkBFhFlbGF6YXJsQGdtYWlsLmNvbTAeFw0x
OTA1MDcxMTUwMThaFw0zOTA1MDIxMTUwMThaMIGOMQswCQYDVQQGEwJJTDEPMA0G
A1UECAwGQ2VudGVyMQwwCgYDVQQHDANMb2QxEDAOBgNVBAoMB0dvUHJveHkxEDAO
BgNVBAsMB0dvUHJveHkxGjAYBgNVBAMMEWdvcHJveHkuZ2l0aHViLmlvMSAwHgYJ
KoZIhvcNAQkBFhFlbGF6YXJsQGdtYWlsLmNvbTBZMBMGByqGSM49AgEGCCqGSM49
AwEHA0IABDlH4YrdukPFAjbO8x+gR9F8ID7eCU8Orhba/MIblSRrRVedpj08lK+2
svyoAcrcDsynClO9aQtsC9ivZ+Pmr3MwCgYIKoZIzj0EAwMDRwAwRAIgGRSSJVSE
1b1KVU0+w+SRtnR5Wb7jkwnaDNxQ3c3FXoICIBJV/l1hFM7mbd68Oi5zLq/4ZsrL
98Bb3nddk2xys6a9
-----END CERTIFICATE-----`)

var EcdsaCaKey = []byte(`-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgEsc8m+2aZfagnesg
qMgXe8ph4LtVu2VOUYhHttuEDsChRANCAAQ5R+GK3bpDxQI2zvMfoEfRfCA+3glP
Dq4W2vzCG5Uka0VXnaY9PJSvtrL8qAHK3A7MpwpTvWkLbAvYr2fj5q9z
-----END PRIVATE KEY-----`)

var EcdsaCa, ecdsaCaErr = tls.X509KeyPair(EcdsaCaCert, EcdsaCaKey)

func init() {
	if ecdsaCaErr != nil {
		panic("Error parsing ecdsa CA " + ecdsaCaErr.Error())
	}
	var err error
	if EcdsaCa.Leaf, err = x509.ParseCertificate(EcdsaCa.Certificate[0]); err != nil {
		panic("Error parsing ecdsa CA " + err.Error())
	}
}
