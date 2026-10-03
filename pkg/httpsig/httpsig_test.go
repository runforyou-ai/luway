package httpsig

import (
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSignMatchesVector 按共享测试向量校验签名请求头逐字节一致。
func TestSignMatchesVector(t *testing.T) {
	seed, err := base64.RawURLEncoding.DecodeString("AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"product":"luway","public_key":"iojj3XQJ8ZX9UtstPLpdcspnCb8dlBIb83SIAbQPb1w","version":"1.5.0"}`
	request := httptest.NewRequest(http.MethodPost, "https://control.example.com/api/v1/instances", strings.NewReader(body))
	signer := Signer{KeyID: "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c", Key: ed25519.NewKeyFromSeed(seed)}
	signer.sign(request, []byte(body), time.Unix(1790000000, 0), "AAECAwQFBgcICQoLDA0ODw")

	want := map[string]string{
		"Content-Digest":  "sha-256=:KUNOydaBvJcT9Kwo1aVPZoEMuOQ4GvxF5cjAPSHPY00=:",
		"Signature-Input": `sig=("@method" "@authority" "@path" "@query" "content-digest");created=1790000000;nonce="AAECAwQFBgcICQoLDA0ODw";keyid="019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c";alg="ed25519"`,
		"Signature":       "sig=:QKyqq6lMzOEmLc8xou78y+sM+ND7akSSwgVq3WxTwH65i9Lskl5/gHyfA/TXsRWXTAYStWDSu2sAx5Goa5k6Bg==:",
	}
	for name, value := range want {
		if got := request.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

// TestAuthorityOmitsDefaultPort 校验目标主机转小写并省略默认端口。
func TestAuthorityOmitsDefaultPort(t *testing.T) {
	for target, want := range map[string]string{
		"https://Control.Example.com:443/x": "control.example.com",
		"http://localhost:80/x":             "localhost",
		"http://localhost:8090/x":           "localhost:8090",
	} {
		if got := authority(httptest.NewRequest(http.MethodGet, target, nil)); got != want {
			t.Errorf("authority(%q) = %q, want %q", target, got, want)
		}
	}
}

// TestTransportSignsEachAttempt 校验传输层按发送的请求体签名，每次发送使用新的 nonce。
func TestTransportSignsEachAttempt(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	signer := Signer{KeyID: "instance", Key: ed25519.NewKeyFromSeed(seed)}
	inputs := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if string(body) != "payload" || request.Header.Get("Content-Digest") == "" {
			t.Errorf("unexpected request body %q or missing digest", body)
		}
		inputs[request.Header.Get("Signature-Input")] = true
	}))
	defer server.Close()
	client := &http.Client{Transport: &Transport{Signer: func(*http.Request) (Signer, error) { return signer, nil }}}
	for range 2 {
		response, err := client.Post(server.URL+"/path?a=1", "text/plain", strings.NewReader("payload"))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if len(inputs) != 2 {
		t.Fatalf("signature inputs = %d, want 2 distinct", len(inputs))
	}
}
