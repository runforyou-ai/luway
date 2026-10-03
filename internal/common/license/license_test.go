package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// contractVector 是与 control 共用的授权码测试向量。
type contractVector struct {
	LicenseCode struct {
		Seed      string         `json:"seed"`
		PublicKey string         `json:"public_key"`
		Header    map[string]any `json:"header"`
		Claims    map[string]any `json:"claims"`
		Code      string         `json:"code"`
	} `json:"license_code"`
}

// loadVector 读取与 control 共用的测试向量。
func loadVector(t *testing.T) contractVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/control-contract-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector contractVector
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	return vector
}

// vectorKeys 返回测试向量的签名私钥和公钥列表。
func vectorKeys(t *testing.T, vector contractVector) (ed25519.PrivateKey, Keys) {
	t.Helper()
	seed, err := base64.RawURLEncoding.DecodeString(vector.LicenseCode.Seed)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := DecodeKeys(map[string]string{vector.LicenseCode.Header["kid"].(string): vector.LicenseCode.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed), keys
}

// sign 以给定头部和声明签发授权码。
func sign(t *testing.T, key ed25519.PrivateKey, header map[string]any, claims map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims(claims))
	for name, value := range header {
		token.Header[name] = value
	}
	code, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// cloneMap 复制一层映射。
func cloneMap(source map[string]any) map[string]any {
	target := make(map[string]any, len(source))
	for name, value := range source {
		target[name] = value
	}
	return target
}

func TestParseContractVector(t *testing.T) {
	vector := loadVector(t)
	_, keys := vectorKeys(t, vector)

	claims, err := Parse(" "+vector.LicenseCode.Code+"\n", keys)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ServerID != "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c" || claims.LicenseID != "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b" || claims.Customer != "武汉润予科技有限公司" {
		t.Fatalf("claims = %+v", claims)
	}
	if !claims.IssuedAt.Equal(time.Unix(1790000000, 0)) || !claims.ExpiresAt.Equal(time.Unix(1821536000, 0)) {
		t.Fatalf("times = %s %s", claims.IssuedAt, claims.ExpiresAt)
	}
	limit, err := WorkspaceLimit(claims.Capabilities[CapabilityWorkspaceLimit])
	if err != nil || limit != 0 {
		t.Fatalf("workspace limit = %d, %v", limit, err)
	}
	branding, err := CustomBranding(claims.Capabilities[CapabilityCustomBranding])
	if err != nil || !branding {
		t.Fatalf("custom branding = %v, %v", branding, err)
	}
}

func TestParseRejectsInvalidCodes(t *testing.T) {
	vector := loadVector(t)
	key, keys := vectorKeys(t, vector)
	_, otherKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	header := vector.LicenseCode.Header
	claims := vector.LicenseCode.Claims
	// with 返回替换一个声明后的声明副本。
	with := func(name string, value any) map[string]any {
		copied := cloneMap(claims)
		copied[name] = value
		return copied
	}
	// withHeader 返回替换一个头部字段后的头部副本。
	withHeader := func(name string, value any) map[string]any {
		copied := cloneMap(header)
		copied[name] = value
		return copied
	}

	cases := map[string]string{
		"empty":               "",
		"malformed":           "not-a-license",
		"tampered":            vector.LicenseCode.Code[:len(vector.LicenseCode.Code)-4] + "AAAA",
		"other signer":        sign(t, otherKey, header, claims),
		"unknown kid":         sign(t, key, withHeader("kid", "k-unknown"), claims),
		"wrong typ":           sign(t, key, withHeader("typ", "JWT"), claims),
		"wrong issuer":        sign(t, key, header, with("iss", "someone-else")),
		"wrong product":       sign(t, key, header, with("aud", "other-product")),
		"bad server":          sign(t, key, header, with("sub", "server")),
		"missing license id":  sign(t, key, header, with("license_id", "")),
		"missing expiry":      sign(t, key, header, with("exp", nil)),
		"limit not integer":   sign(t, key, header, with("capabilities", map[string]any{CapabilityWorkspaceLimit: "3"})),
		"limit negative":      sign(t, key, header, with("capabilities", map[string]any{CapabilityWorkspaceLimit: -1})),
		"branding not a bool": sign(t, key, header, with("capabilities", map[string]any{CapabilityCustomBranding: 1})),
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(code, keys); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseKeepsUnknownCapabilities(t *testing.T) {
	vector := loadVector(t)
	key, keys := vectorKeys(t, vector)
	claims := cloneMap(vector.LicenseCode.Claims)
	claims["capabilities"] = map[string]any{"push.relay": true}

	parsed, err := Parse(sign(t, key, vector.LicenseCode.Header, claims), keys)
	if err != nil {
		t.Fatal(err)
	}
	if string(parsed.Capabilities["push.relay"]) != "true" || len(parsed.Capabilities) != 1 {
		t.Fatalf("capabilities = %s", parsed.Capabilities)
	}
}

func TestPublicKeysDecode(t *testing.T) {
	keys, err := DecodeKeys(encodedPublicKeys)
	if err != nil || len(keys) == 0 {
		t.Fatalf("keys = %d, err = %v", len(keys), err)
	}
}
