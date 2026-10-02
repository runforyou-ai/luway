package customeridentity

import "testing"

// TestParseExternalID 验证匿名访客与登录用户外部编号的生成、解析与格式校验。
func TestParseExternalID(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef"
	if kind, id, ok := ParseExternalID(AnonymousExternalID(token)); !ok || kind != ExternalIDAnonymous || id != token {
		t.Fatalf("anonymous = %v %q %v", kind, id, ok)
	}
	if kind, id, ok := ParseExternalID(CustomerExternalID("user-42@example.com")); !ok || kind != ExternalIDCustomer || id != "user-42@example.com" {
		t.Fatalf("customer = %v %q %v", kind, id, ok)
	}
	for _, invalid := range []string{"web-session:gggggggggggggggggggggggggggggggg", "web-session:abc", "web-user:", "web-user:a b", "12345"} {
		if ValidExternalID(invalid) {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
	if kind, _, ok := ParseExternalID("web-user:a b"); ok || kind != 0 {
		t.Fatalf("invalid customer = %v %v", kind, ok)
	}
	if ValidAnonymousToken("gggggggggggggggggggggggggggggggg") || !ValidAnonymousToken(token) {
		t.Fatal("unexpected anonymous token validation")
	}
	if kind, userID, ok := ParseExternalID(CustomerExternalID("user-42")); !ok || kind != ExternalIDCustomer || userID != "user-42" {
		t.Fatalf("customer external ID = %v %q %v", kind, userID, ok)
	}
}
