//go:build server

package inbox

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestInboxCursor 验证微秒与空时间无损往返，并拒绝失效排序版本和筛选。
func TestInboxCursor(t *testing.T) {
	identity := &servermodels.Identity{Organization: servermodels.Organization{ID: "organization"}, User: servermodels.User{ID: "user"}}
	input := LoadInput{
		Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity,
		AssigneeIdentityID: "019d4e1c-40a5-77dd-82e6-6951f9957ba5", ChannelID: "019d4e1c-40a5-77dd-82e6-6951f9957ba7",
		Audience: domain.ServiceAudienceCustomer, ServiceStatus: domain.ServiceSessionStatusClosed,
	}
	activity := time.Date(2026, 9, 9, 0, 0, 0, 123456000, time.UTC)
	for _, value := range []*time.Time{nil, &activity} {
		point := inboxCursorPoint{ID: "019d4e1c-40a5-77dd-82e6-6951f9957ba6", LastActivityAt: value}
		encoded, err := encodeInboxCursor(identity, input, 0, point)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeInboxCursor(encoded, identity, input)
		if err != nil || decoded.ID != point.ID || (decoded.LastActivityAt == nil) != (value == nil) || (value != nil && !decoded.LastActivityAt.Equal(*value)) {
			t.Fatalf("round trip=%+v err=%v", decoded, err)
		}
		// 接受的 UUID 大小写形式统一回数据库小写值，保证区间比较与锚点匹配一致。
		uppercase := *decoded
		uppercase.ID = strings.ToUpper(point.ID)
		data, err := json.Marshal(uppercase)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := decodeInboxCursor(base64.RawURLEncoding.EncodeToString(data), identity, input)
		if err != nil || canonical.ID != point.ID {
			t.Fatalf("uppercase cursor=%+v err=%v", canonical, err)
		}
		// 续页使用相同筛选条件和边界，页大小可独立调整。
		resized := input
		resized.Limit = 1
		if _, err := decodeInboxCursor(encoded, identity, resized); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*inboxCursor){
			func(c *inboxCursor) { c.Version++ },
			func(c *inboxCursor) { c.OrganizationID = "another" },
			func(c *inboxCursor) { c.UserID = "another" },
			func(c *inboxCursor) { c.Scope = domain.InboxScopePending },
			func(c *inboxCursor) { c.AssigneeFilter = domain.InboxAssigneeFilterUnassigned },
			func(c *inboxCursor) { c.AssigneeIdentityID = "" },
			func(c *inboxCursor) { c.Audience = "" },
			func(c *inboxCursor) { c.ChannelID = "" },
			func(c *inboxCursor) { c.ServiceStatus = domain.ServiceSessionStatusOpen },
			func(c *inboxCursor) { c.Kinds = []domain.ConversationType{domain.ConversationTypeGroup} },
			func(c *inboxCursor) { c.PendingKind = domain.InboxPendingKindReply },
			func(c *inboxCursor) { c.Partition = domain.InboxPartitionPinned },
			func(c *inboxCursor) { c.ID = "bad" },
		} {
			changed := *decoded
			mutate(&changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeInboxCursor(base64.RawURLEncoding.EncodeToString(data), identity, input); !errors.Is(err, ErrCursorInvalid) {
				t.Fatalf("accepted invalid cursor=%+v err=%v", changed, err)
			}
		}
	}
	for _, value := range []string{"!", "bnVsbA", base64.RawURLEncoding.EncodeToString([]byte(`{"version":2,"lastActivityAt":"bad-time"}`))} {
		if _, err := decodeInboxCursor(value, identity, input); !errors.Is(err, ErrCursorInvalid) {
			t.Fatalf("accepted malformed cursor=%s err=%v", value, err)
		}
	}
}

// TestPinnedInboxCursor 验证置顶区游标绑定个人顺序版本，版本变化或缺少顺序值时要求重读。
func TestPinnedInboxCursor(t *testing.T) {
	identity := &servermodels.Identity{Organization: servermodels.Organization{ID: "organization"}, User: servermodels.User{ID: "user"}}
	input := LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	rank := int64(1 << 20)
	point := inboxCursorPoint{ID: "019d4e1c-40a5-77dd-82e6-6951f9957ba6", PinRank: &rank}
	encoded, err := encodeInboxCursor(identity, input, 7, point)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := decodeInboxCursor(encoded, identity, input)
	if err != nil || cursor.PinRank == nil || *cursor.PinRank != rank || cursor.PinOrderVersion != 7 {
		t.Fatalf("pinned cursor=%+v err=%v", cursor, err)
	}
	if err := authorizeInboxCursor(cursor, domain.InboxPartitionPinned, 7); err != nil {
		t.Fatalf("rejected current pin order version: %v", err)
	}
	if err := authorizeInboxCursor(cursor, domain.InboxPartitionPinned, 8); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("accepted stale pin order version: %v", err)
	}
	// 普通活动序游标进入置顶区时没有顺序值，同样要求整区重读。
	regular := LoadInput{Scope: domain.InboxScopeChat}
	activity := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	encoded, err = encodeInboxCursor(identity, regular, 0, inboxCursorPoint{ID: point.ID, LastActivityAt: &activity})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err = decodeInboxCursor(encoded, identity, regular)
	if err != nil {
		t.Fatal(err)
	}
	if err := authorizeInboxCursor(cursor, domain.InboxPartitionPinned, 0); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("accepted cursor without pin rank: %v", err)
	}
	if err := authorizeInboxCursor(cursor, domain.InboxPartitionAll, 3); err != nil {
		t.Fatalf("rejected activity cursor outside pinned partition: %v", err)
	}
}

// TestPendingInboxCursor 验证待处理游标按等待起点正序往返，缺少等待起点的游标要求重读。
func TestPendingInboxCursor(t *testing.T) {
	identity := &servermodels.Identity{Organization: servermodels.Organization{ID: "organization"}, User: servermodels.User{ID: "user"}}
	input := LoadInput{Scope: domain.InboxScopePending, PendingKind: domain.InboxPendingKindQueue, QueueFilter: domain.ServiceQueueFilterPublic}
	earlier := time.Date(2026, 9, 9, 0, 0, 0, 123456000, time.UTC)
	later := earlier.Add(time.Minute)
	first := inboxCursorPoint{ID: "019d4e1c-40a5-77dd-82e6-6951f9957ba6", WaitingSince: &earlier}
	second := inboxCursorPoint{ID: "019d4e1c-40a5-77dd-82e6-6951f9957ba5", WaitingSince: &later}
	if compareInboxPoints(input.order(), first, second) >= 0 {
		t.Fatal("longer waiting item must come first")
	}
	encoded, err := encodeInboxCursor(identity, input, 0, first)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := decodeInboxCursor(encoded, identity, input)
	if err != nil || cursor.WaitingSince == nil || !cursor.WaitingSince.Equal(earlier) {
		t.Fatalf("pending cursor=%+v err=%v", cursor, err)
	}
	encoded, err = encodeInboxCursor(identity, input, 0, inboxCursorPoint{ID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeInboxCursor(encoded, identity, input); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("accepted pending cursor without waiting start: %v", err)
	}
}
