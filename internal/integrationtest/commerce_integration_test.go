//go:build server

package integrationtest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	commerceaction "github.com/runforyou-ai/luway/internal/actions/commerce"
	creditaction "github.com/runforyou-ai/luway/internal/actions/credit"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/commerce"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/httpsig"
	"github.com/uptrace/bun"
)

// fakeCommerce 模拟商业服务：校验服务器签名后确认配对，同一实例可重复确认同一令牌，并按序号返回变更源。
type fakeCommerce struct {
	mu          sync.Mutex
	t           *testing.T
	serverID    string
	serverKey   ed25519.PublicKey
	tokens      map[string]ed25519.PublicKey
	changes     []map[string]any
	unavailable bool
}

// ServeHTTP 用请求体或已登记的服务器公钥验签后，按路径模拟配对确认与变更源读取。
func (f *fakeCommerce) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(request.Body)
	if f.unavailable {
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": "service_unavailable", "status": 503, "retryable": true})
		return
	}
	switch request.URL.Path {
	case "/api/v1/pairing/confirm":
		var input struct {
			Product   string `json:"product"`
			Token     string `json:"token"`
			PublicKey string `json:"public_key"`
		}
		_ = json.Unmarshal(body, &input)
		publicKey, _ := base64.RawURLEncoding.DecodeString(input.PublicKey)
		if err := httpsig.Verify(request, body, f.serverID, publicKey, time.Now(), 5*time.Minute); err != nil {
			f.t.Errorf("pairing signature: %v", err)
			writeProblem(writer, http.StatusUnauthorized, "invalid_signature")
			return
		}
		// 令牌被其他实例公钥确认过时拒绝，同一实例重复确认时返回成功。
		confirmedBy, found := f.tokens[input.Token]
		if input.Product != "luway" || !found || confirmedBy != nil && !confirmedBy.Equal(ed25519.PublicKey(publicKey)) {
			writeProblem(writer, http.StatusUnprocessableEntity, "pairing_token_invalid")
			return
		}
		f.tokens[input.Token] = publicKey
		f.serverKey = publicKey
		writer.WriteHeader(http.StatusNoContent)
	case "/api/v1/changes":
		if err := httpsig.Verify(request, body, f.serverID, f.serverKey, time.Now(), 5*time.Minute); err != nil {
			f.t.Errorf("changes signature: %v", err)
			writeProblem(writer, http.StatusUnauthorized, "invalid_signature")
			return
		}
		after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		data := make([]map[string]any, 0)
		for index, change := range f.changes {
			if sequence := int64(index + 1); sequence > after && len(data) < limit {
				change["sequence"] = sequence
				data = append(data, change)
			}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": data})
	default:
		writeProblem(writer, http.StatusNotFound, "not_found")
	}
}

// pairingCode 返回指向 fake 地址、带指定公钥与令牌的配对码，并登记令牌可用。
func (f *fakeCommerce) pairingCode(url string, publicKey ed25519.PublicKey, token string) string {
	f.mu.Lock()
	f.tokens[token] = nil
	f.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{
		"url": url + "/", "service_id": "commerce-test", "public_key": base64.RawURLEncoding.EncodeToString(publicKey), "token": token,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestCommerce 验证配对码校验与签名确认、变更源按序应用权益与积分充值订单、读取失败记录、重新配对从头读取、通知验签与解除配对。
func TestCommerce(t *testing.T) {
	t.Parallel()
	commercePublic, commercePrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	db := openEmptyDatabase(t)
	fake := &fakeCommerce{t: t, tokens: map[string]ed25519.PublicKey{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	client := commerce.New(func(ctx context.Context) (commerce.Identity, error) {
		return commerceaction.Identity(ctx, db)
	})
	tasks := newTestTasks(db)
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL, Commerce: client},
		nil, serverfilecontent.S3Config{}, nil, nil, tasks, nil, nil, nil)
	service := appservice.New(backend)
	ctx := context.Background()
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		WorkspaceName: "商业服务", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	current, err := service.GetLicense(ctx, adminMeta)
	if err != nil {
		t.Fatal(err)
	}
	fake.serverID = current.ServerID
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	organizationID := workspaces.Items[0].ID
	memberMeta := appservice.RequestMeta{Token: admin.Token, WorkspaceID: organizationID, Locale: appservice.LocaleChineseSimplified}

	// 未配对时状态为空。
	pairing, err := service.GetCommercePairing(ctx, adminMeta)
	if err != nil || pairing.Paired {
		t.Fatalf("initial pairing = %#v, err = %v", pairing, err)
	}

	// 配对码格式错误或令牌不可用时返回对应错误。
	_, err = backend.PairCommerce(ctx, adminMeta, appservice.PairCommerceInput{PairingCode: "not-a-code"})
	requireErrorMessage(t, err, i18n.ErrorCommercePairingCodeInvalid)
	unknown, _ := json.Marshal(map[string]string{
		"url": server.URL, "service_id": "commerce-test", "public_key": base64.RawURLEncoding.EncodeToString(commercePublic), "token": "unknown",
	})
	_, err = backend.PairCommerce(ctx, adminMeta, appservice.PairCommerceInput{PairingCode: base64.RawURLEncoding.EncodeToString(unknown)})
	requireErrorMessage(t, err, i18n.ErrorCommercePairingRejected)

	// 配对成功后保存商业服务地址与标识；同一配对码可重复确认，用于确认后保存失败时重试。
	code := fake.pairingCode(server.URL, commercePublic, "first")
	pairing, err = service.PairCommerce(ctx, adminMeta, appservice.PairCommerceInput{PairingCode: " " + code + "\n"})
	if err != nil || !pairing.Paired || pairing.URL != server.URL || pairing.ServiceID != "commerce-test" || pairing.PairedAt == nil || pairing.SyncedAt != nil {
		t.Fatalf("paired = %#v, err = %v", pairing, err)
	}
	if _, err := backend.PairCommerce(ctx, adminMeta, appservice.PairCommerceInput{PairingCode: code}); err != nil {
		t.Fatalf("retry pairing: %v", err)
	}

	// 变更按序应用：较低版本的权益丢弃，重复的已支付订单只入账一次，未知类型与不存在的工作区跳过。
	periodEnd := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	entitlement := func(revision int64, plan string, seats int) map[string]any {
		return map[string]any{"type": "entitlement", "workspace_id": organizationID, "occurred_at": time.Now(), "entitlement": map[string]any{
			"revision": revision, "plan": map[string]any{"id": plan, "name": plan}, "seat_limit": seats, "period_end": periodEnd,
		}}
	}
	creditOrder := func(workspaceID, orderID string, credits int64, status string) map[string]any {
		return map[string]any{"type": "credit_order", "workspace_id": workspaceID, "occurred_at": time.Now(), "credit_order": map[string]any{
			"order_id": orderID, "credits": credits, "status": status,
		}}
	}
	fake.mu.Lock()
	fake.changes = []map[string]any{
		entitlement(2, "专业版", 20),
		entitlement(1, "免费版", 3),
		creditOrder(organizationID, "order-a", 500, "paid"),
		creditOrder(organizationID, "order-a", 500, "paid"),
		creditOrder("01900000-0000-7000-8000-000000000000", "order-x", 900, "paid"),
		{"type": "future", "workspace_id": organizationID, "occurred_at": time.Now()},
		{"type": "future", "occurred_at": time.Now()},
	}
	fake.mu.Unlock()
	pairing, err = service.SyncCommerce(ctx, adminMeta)
	if err != nil || pairing.SyncedAt == nil || pairing.FailedAt != nil {
		t.Fatalf("synced = %#v, err = %v", pairing, err)
	}
	requireEntitlement := func(revision int64, plan string, seats int) {
		t.Helper()
		got := &servermodels.WorkspaceEntitlement{}
		if err := db.NewSelect().Model(got).Where("organization_id = ?", organizationID).Scan(ctx); err != nil ||
			got.Revision != revision || got.PlanName != plan || got.SeatLimit != seats || got.PeriodEnd == nil || !got.PeriodEnd.Equal(periodEnd) {
			t.Fatalf("entitlement = %#v, err = %v", got, err)
		}
	}
	requireEntitlement(2, "专业版", 20)
	requireBalance := func(want int64) {
		t.Helper()
		got, err := backend.GetCreditBalance(ctx, memberMeta)
		if err != nil || got.Available != want {
			t.Fatalf("balance = %#v, err = %v, want %d", got, err, want)
		}
	}
	requireBalance(500)
	var sequence int64
	if err := db.NewSelect().Model((*servermodels.CommercePairing)(nil)).Column("change_sequence").Scan(ctx, &sequence); err != nil || sequence != 7 {
		t.Fatalf("sequence = %d, err = %v", sequence, err)
	}

	// 退款扣回该批次剩余积分，已消费部分不追回，重复退款不再变动；更高版本的权益覆盖当前权益。
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := creditaction.Adjust(ctx, tx, organizationID, admin.Account.ID, -200, "消费", time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.changes = append(fake.changes,
		creditOrder(organizationID, "order-a", 500, "refunded"),
		creditOrder(organizationID, "order-a", 500, "refunded"),
		creditOrder(organizationID, "order-b", 50, "paid"),
		entitlement(3, "企业版", 0),
	)
	fake.mu.Unlock()
	if _, err := service.SyncCommerce(ctx, adminMeta); err != nil {
		t.Fatal(err)
	}
	requireBalance(50)
	requireEntitlement(3, "企业版", 0)
	entries, err := backend.ListCreditEntries(ctx, memberMeta, appservice.CreditEntryListInput{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[appservice.CreditEntryKind][]int64{}
	for _, entry := range entries.Entries {
		kinds[entry.Kind] = append(kinds[entry.Kind], entry.Amount)
	}
	if len(kinds[appservice.CreditEntryKindPurchase]) != 2 || len(kinds[appservice.CreditEntryKindRefund]) != 1 ||
		kinds[appservice.CreditEntryKindRefund][0] != -300 || len(kinds[appservice.CreditEntryKindExpiration]) != 0 {
		t.Fatalf("entries = %#v", kinds)
	}

	// 已知类型的变更不符合约定、同号订单金额不一致或退款找不到充值时整页不应用、不推进序号，并记录失败原因；修正后继续。
	for _, invalid := range []map[string]any{
		{"type": "credit_order", "workspace_id": organizationID, "occurred_at": time.Now()},
		creditOrder(organizationID, "order-c", 0, "paid"),
		creditOrder("not-a-uuid", "order-c", 10, "paid"),
		creditOrder(organizationID, "order-never-paid", 10, "refunded"),
		creditOrder(organizationID, "order-a", 999, "paid"),
		{"type": "credit_order", "workspace_id": organizationID, "occurred_at": time.Now(), "entitlement": map[string]any{
			"revision": 99, "plan": map[string]any{"id": "x", "name": "x"}, "seat_limit": 1,
		}},
	} {
		fake.mu.Lock()
		fake.changes = append(fake.changes, creditOrder(organizationID, "order-c", 10, "paid"), invalid)
		fake.mu.Unlock()
		_, err = backend.SyncCommerce(ctx, adminMeta)
		requireErrorMessage(t, err, i18n.ErrorCommerceInvalidData)
		pairing, err = service.GetCommercePairing(ctx, adminMeta)
		if err != nil || pairing.Failure == nil || *pairing.Failure != appservice.CommerceSyncFailureInvalidData {
			t.Fatalf("invalid change pairing = %#v, err = %v", pairing, err)
		}
		requireBalance(50)
		fake.mu.Lock()
		fake.changes = fake.changes[:len(fake.changes)-2]
		fake.mu.Unlock()
	}

	// 商业服务不可用时记录失败原因，恢复后清空。
	fake.mu.Lock()
	fake.unavailable = true
	fake.mu.Unlock()
	_, err = backend.SyncCommerce(ctx, adminMeta)
	requireErrorMessage(t, err, i18n.ErrorCommerceUnavailable)
	pairing, err = service.GetCommercePairing(ctx, adminMeta)
	if err != nil || pairing.FailedAt == nil || pairing.Failure == nil || *pairing.Failure != appservice.CommerceSyncFailureUnavailable {
		t.Fatalf("failed pairing = %#v, err = %v", pairing, err)
	}
	fake.mu.Lock()
	fake.unavailable = false
	fake.mu.Unlock()
	if err := commerceaction.NewSyncChangesAction(db, client).Execute(ctx, commerceaction.SyncChangesInput{}); err != nil {
		t.Fatal(err)
	}
	pairing, err = service.GetCommercePairing(ctx, adminMeta)
	if err != nil || pairing.FailedAt != nil || pairing.Failure != nil {
		t.Fatalf("recovered pairing = %#v, err = %v", pairing, err)
	}

	// 通知只接受已配对商业服务的签名。
	notify := commerceaction.NewReceiveNotificationAction(db, tasks)
	signed := func(key ed25519.PrivateKey, keyID string) *http.Request {
		request := httptest.NewRequest(http.MethodPost, "https://luway.example.test/api/integrations/commerce/notify", bytes.NewReader(nil))
		if err := (httpsig.Signer{KeyID: keyID, Key: key}).Sign(request, nil); err != nil {
			t.Fatal(err)
		}
		return request
	}
	if err := notify.Execute(ctx, signed(commercePrivate, "commerce-test"), nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	_, otherKey, _ := ed25519.GenerateKey(nil)
	for name, request := range map[string]*http.Request{
		"key":   signed(otherKey, "commerce-test"),
		"keyid": signed(commercePrivate, "other"),
	} {
		if err := notify.Execute(ctx, request, nil); !errors.Is(err, commerceaction.ErrNotificationUnauthorized) {
			t.Fatalf("notify with wrong %s err = %v", name, err)
		}
	}

	// 重新配对后从头读取，权益恢复，积分不重复入账。
	if _, err := db.NewUpdate().Model((*servermodels.WorkspaceEntitlement)(nil)).Set("revision = 9").Where("true").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PairCommerce(ctx, adminMeta, appservice.PairCommerceInput{PairingCode: fake.pairingCode(server.URL, commercePublic, "second")}); err != nil {
		t.Fatal(err)
	}
	if exists, err := db.NewSelect().Model((*servermodels.WorkspaceEntitlement)(nil)).Exists(ctx); err != nil || exists {
		t.Fatalf("entitlements after re-pair exists = %v, err = %v", exists, err)
	}
	if _, err := service.SyncCommerce(ctx, adminMeta); err != nil {
		t.Fatal(err)
	}
	requireEntitlement(3, "企业版", 0)
	requireBalance(50)

	// 解除配对删除配对与权益，充值积分保留，之后的通知被拒绝。
	if err := service.UnpairCommerce(ctx, adminMeta); err != nil {
		t.Fatal(err)
	}
	pairing, err = service.GetCommercePairing(ctx, adminMeta)
	if err != nil || pairing.Paired {
		t.Fatalf("unpaired = %#v, err = %v", pairing, err)
	}
	if exists, err := db.NewSelect().Model((*servermodels.WorkspaceEntitlement)(nil)).Exists(ctx); err != nil || exists {
		t.Fatalf("entitlements after unpair exists = %v, err = %v", exists, err)
	}
	requireBalance(50)
	if err := notify.Execute(ctx, signed(commercePrivate, "commerce-test"), nil); !errors.Is(err, commerceaction.ErrNotificationUnauthorized) {
		t.Fatalf("notify after unpair err = %v", err)
	}
	_, err = backend.SyncCommerce(ctx, adminMeta)
	requireErrorMessage(t, err, i18n.ErrorCommerceNotPaired)
}
