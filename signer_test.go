package goproxy

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func orFatal(msg string, err error, t *testing.T) {
	if err != nil {
		t.Fatal(msg, err)
	}
}

type ConstantHanlder string

func (h ConstantHanlder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(h))
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
	cert, err := signHost(ca, []string{"example.com", "1.1.1.1", "localhost"})
	orFatal("singHost", err, t)
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	orFatal("ParseCertificate", err, t)
	certpool := x509.NewCertPool()
	certpool.AddCert(ca.Leaf)
	orFatal("VerifyHostname", cert.Leaf.VerifyHostname("example.com"), t)
	orFatal("CheckSignatureFrom", cert.Leaf.CheckSignatureFrom(ca.Leaf), t)
	_, err = cert.Leaf.Verify(x509.VerifyOptions{
		DNSName: "example.com",
		Roots:   certpool,
	})
	orFatal("Verify", err, t)
}

func testSignerTls(t *testing.T, ca tls.Certificate) {
	cert, err := signHost(ca, []string{"example.com", "1.1.1.1", "localhost"})
	orFatal("singHost", err, t)
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	orFatal("ParseCertificate", err, t)
	expected := "key verifies with Go"
	server := httptest.NewUnstartedServer(ConstantHanlder(expected))
	defer server.Close()
	server.TLS = &tls.Config{Certificates: []tls.Certificate{*cert, ca}}
	server.TLS.BuildNameToCertificate()
	server.StartTLS()
	certpool := x509.NewCertPool()
	certpool.AddCert(ca.Leaf)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: certpool},
	}
	asLocalhost := strings.Replace(server.URL, "127.0.0.1", "localhost", -1)
	req, err := http.NewRequest("GET", asLocalhost, nil)
	orFatal("NewRequest", err, t)
	resp, err := tr.RoundTrip(req)
	orFatal("RoundTrip", err, t)
	txt, err := ioutil.ReadAll(resp.Body)
	orFatal("ioutil.ReadAll", err, t)
	if string(txt) != expected {
		t.Errorf("Expected '%s' got '%s'", expected, string(txt))
	}
	browser := getBrowser(os.Args)
	if browser != "" {
		exec.Command(browser, asLocalhost).Run()
		time.Sleep(10 * time.Second)
	}
}

func TestSignerRsaTls(t *testing.T) {
	testSignerTls(t, GoproxyCa)
}

func TestSignerRsaX509(t *testing.T) {
	testSignerX509(t, GoproxyCa)
}

func TestSignerEcdsaTls(t *testing.T) {
	testSignerTls(t, EcdsaCa)
}

func TestSignerEcdsaX509(t *testing.T) {
	testSignerX509(t, EcdsaCa)
}

var c *tls.Certificate
var e error

func BenchmarkSignRsa(b *testing.B) {
	var cert *tls.Certificate
	var err error
	for n := 0; n < b.N; n++ {
		cert, err = signHost(GoproxyCa, []string{"example.com", "1.1.1.1", "localhost"})

	}
	c = cert
	e = err
}

func BenchmarkSignEcdsa(b *testing.B) {
	var cert *tls.Certificate
	var err error
	for n := 0; n < b.N; n++ {
		cert, err = signHost(EcdsaCa, []string{"example.com", "1.1.1.1", "localhost"})

	}
	c = cert
	e = err
}

//
// Eliptic Curve certificate and key for testing
//

var ECDSA_CA_CERT = []byte(`-----BEGIN CERTIFICATE-----
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

var ECDSA_CA_KEY = []byte(`-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgEsc8m+2aZfagnesg
qMgXe8ph4LtVu2VOUYhHttuEDsChRANCAAQ5R+GK3bpDxQI2zvMfoEfRfCA+3glP
Dq4W2vzCG5Uka0VXnaY9PJSvtrL8qAHK3A7MpwpTvWkLbAvYr2fj5q9z
-----END PRIVATE KEY-----`)

var EcdsaCa, ecdsaCaErr = tls.X509KeyPair(ECDSA_CA_CERT, ECDSA_CA_KEY)

func init() {
	if ecdsaCaErr != nil {
		panic("Error parsing ecdsa CA " + ecdsaCaErr.Error())
	}
	var err error
	if EcdsaCa.Leaf, err = x509.ParseCertificate(EcdsaCa.Certificate[0]); err != nil {
		panic("Error parsing ecdsa CA " + err.Error())
	}
}
