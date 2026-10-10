//go:build server

package direct

import (
	"errors"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// conversationConflictRule 把会话冲突按原因码转换为业务冲突，未登记的原因使用 fallback 文案。
func conversationConflictRule(fallback i18n.Key, keys map[string]i18n.Key) dispatch.Rule {
	return func(meta appservice.RequestMeta, err error) error {
		if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
			key, exists := keys[conflictError.Reason]
			if !exists {
				key = fallback
			}
			return appservice.ConflictError(meta, key, conflictError.Reason)
		}
		return nil
	}
}
