//go:build server

package api

import (
	"testing"

	"github.com/runforyou-ai/luway/internal/common/customeridentity"
)

// TestGenerateWebsiteVisitorToken 验证生成的访客令牌符合匿名访客令牌格式。
func TestGenerateWebsiteVisitorToken(t *testing.T) {
	token, err := generateWebsiteVisitorToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if !customeridentity.ValidAnonymousToken(token) {
		t.Fatalf("invalid token format: %q", token)
	}
}
