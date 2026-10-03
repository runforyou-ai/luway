package httpsig

import (
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	request := httptest.NewRequest(http.MethodPost, "https://control.example.com/api/v1/servers", strings.NewReader(body))
	signer := Signer{KeyID: "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c", Key: ed25519.NewKeyFromSeed(seed)}
	signer.sign(request, []byte(body), time.Unix(1790000000, 0), "AAECAwQFBgcICQoLDA0ODw")

	want := map[string]string{
		"Content-Digest":  "sha-256=:KUNOydaBvJcT9Kwo1aVPZoEMuOQ4GvxF5cjAPSHPY00=:",
		"Signature-Input": `sig=("@method" "@authority" "@path" "@query" "content-digest");created=1790000000;nonce="AAECAwQFBgcICQoLDA0ODw";keyid="019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c";alg="ed25519"`,
		"Signature":       "sig=:fbzvO7VqYjgTSwE2atX7X1hWKDvhIsAmkLLGOFMbrRzTX+xpAN0kf66WSqYgH1LMBhTao/quBecJjmZW/pWCAw==:",
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
	signer := Signer{KeyID: "server", Key: ed25519.NewKeyFromSeed(seed)}
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

// TestVerifyAcceptsSignedRequest 校验签名后的请求在服务端按原始请求目标验签通过，篡改、换钥和超时均被拒绝。
func TestVerifyAcceptsSignedRequest(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	key := ed25519.NewKeyFromSeed(seed)
	signer := Signer{KeyID: "commerce", Key: key}
	body := []byte("")
	now := time.Unix(1790000000, 0)
	outgoing := httptest.NewRequest(http.MethodPost, "https://luway.example.com/api/notify?x=1", nil)
	signer.sign(outgoing, body, now, "nonce")

	received := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, "/api/notify?x=1", nil)
		request.Host = "luway.example.com"
		request.Header = outgoing.Header.Clone()
		// 服务端路由剥离前缀后，验签仍按原始请求目标计算路径。
		request.URL.Path = "/notify"
		return request
	}
	public := key.Public().(ed25519.PublicKey)
	if err := Verify(received(), body, "commerce", public, now.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	// 签名方选用其他标签时同样验签通过。
	relabeled := received()
	relabeled.Header.Set("Signature-Input", strings.Replace(relabeled.Header.Get("Signature-Input"), "sig=", "commerce=", 1))
	relabeled.Header.Set("Signature", strings.Replace(relabeled.Header.Get("Signature"), "sig=", "commerce=", 1))
	if err := Verify(relabeled, body, "commerce", public, now, 5*time.Minute); err != nil {
		t.Fatalf("Verify() with custom label error = %v", err)
	}
	// 签名方按其他顺序覆盖组成部分并额外覆盖请求头时，按声明顺序验签通过。
	reordered := received()
	reordered.Header.Set("Actor", `user="u1"`)
	params := `("@path" "@method" "actor" "@query" "@authority" "content-digest");created=` + strconv.FormatInt(now.Unix(), 10) + `;nonce="n2";keyid="commerce";alg="ed25519"`
	base := strings.Join([]string{
		`"@path": /api/notify`, `"@method": POST`, `"actor": user="u1"`, `"@query": ?x=1`, `"@authority": luway.example.com`,
		`"content-digest": ` + reordered.Header.Get("Content-Digest"), `"@signature-params": ` + params,
	}, "\n")
	reordered.Header.Set("Signature-Input", "sig="+params)
	reordered.Header.Set("Signature", "sig=:"+base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(base)))+":")
	if err := Verify(reordered, body, "commerce", public, now, 5*time.Minute); err != nil {
		t.Fatalf("Verify() with reordered components error = %v", err)
	}
	other := ed25519.NewKeyFromSeed(append(make([]byte, ed25519.SeedSize-1), 1)).Public().(ed25519.PublicKey)
	for name, check := range map[string]func() error{
		"body":  func() error { return Verify(received(), []byte("x"), "commerce", public, now, 5*time.Minute) },
		"keyid": func() error { return Verify(received(), body, "other", public, now, 5*time.Minute) },
		"key":   func() error { return Verify(received(), body, "commerce", other, now, 5*time.Minute) },
		"expired": func() error {
			return Verify(received(), body, "commerce", public, now.Add(10*time.Minute), 5*time.Minute)
		},
		"method": func() error {
			request := received()
			request.Method = http.MethodGet
			return Verify(request, body, "commerce", public, now, 5*time.Minute)
		},
	} {
		if err := check(); err == nil {
			t.Errorf("%s: Verify() accepted tampered request", name)
		}
	}
}
