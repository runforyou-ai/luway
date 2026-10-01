//go:build server

package direct

import (
	"context"
	"errors"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// TestBackendPreservesCancellation 验证请求取消原因的透传。
func TestBackendPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&directOperations{}).contactError(ctx, appservice.RequestMeta{}, errors.New("query failed"), i18n.ErrorContactReadFailed)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
