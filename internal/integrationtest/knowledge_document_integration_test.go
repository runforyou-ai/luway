//go:build server

package integrationtest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	filecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// newDocumentFixture 创建独立工作区和标准知识库。
func newDocumentFixture(t *testing.T, db *bun.DB) (installedWorkspace, *knowledgeaction.Record) {
	t.Helper()
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "文档测试", DisplayName: "维护人员", Email: uniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(context.Background(), installed.Identity, newKnowledgeBaseInput(t, db, installed.Identity, "资料", domain.KnowledgeBaseCategoryStandard))
	if err != nil {
		t.Fatal(err)
	}
	return installed, base
}

// uploadedDocumentFile 创建已核验上传的测试原件。
func uploadedDocumentFile(t *testing.T, db *bun.DB, identity *servermodels.Identity, name string) *servermodels.File {
	t.Helper()
	ctx := context.Background()
	record, err := fileaction.NewCreateUploadAction(db).Execute(ctx, identity, domain.FileStorageBackendLocal, fileaction.UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: name, ContentType: "application/octet-stream", ByteSize: 12})
	if err != nil {
		t.Fatal(err)
	}
	record, err = markFileUploaded(ctx, db, identity, record.ID, "etag")
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// TestKnowledgeDocumentLifecycle 验证批次幂等、知识库内倒序分页及删除原件状态。
func TestKnowledgeDocumentLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	first := uploadedDocumentFile(t, db, identity, "报表100%.XLSX")
	second := uploadedDocumentFile(t, db, identity, "说明.pdf")
	create := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db))
	query := knowledgeaction.NewDocumentQuery(db)
	docs, err := create.Execute(ctx, identity, base.ID, []string{first.ID, second.ID})
	if err != nil || len(docs) != 2 {
		t.Fatalf("create=%+v %v", docs, err)
	}
	repeat, err := create.Execute(ctx, identity, base.ID, []string{first.ID, second.ID})
	if err != nil || repeat[0].ID != docs[0].ID {
		t.Fatalf("retry=%+v %v", repeat, err)
	}
	page, err := query.List(ctx, identity, base.ID, knowledgeaction.DocumentListInput{PageSize: 1})
	if err != nil || page.Total != 2 || page.Documents[0].ID != docs[1].ID {
		t.Fatalf("page=%+v %v", page, err)
	}
	for keyword, count := range map[string]int{"100%": 1, "_": 0, "报表": 1} {
		result, err := query.List(ctx, identity, base.ID, knowledgeaction.DocumentListInput{Keyword: keyword})
		if err != nil || result.Total != count {
			t.Fatalf("search %s=%+v %v", keyword, result, err)
		}
	}
	if docs[0].Status != domain.KnowledgeIndexQueued {
		t.Fatal("new document not queued")
	}
	detail, err := query.Get(ctx, identity, base.ID, docs[0].ID)
	if err != nil || detail.ID != docs[0].ID || detail.Name != docs[0].Name {
		t.Fatalf("detail=%+v %v", detail, err)
	}
	if _, err := knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, newKnowledgeBaseInput(t, db, identity, base.Name, domain.KnowledgeBaseCategoryQA)); !errors.Is(err, knowledgeaction.ErrBaseHasContent) {
		t.Fatal("occupied base", err)
	}
	if err := knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, identity, base.ID, docs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := query.File(ctx, identity, base.ID, docs[0].ID); !errors.Is(err, knowledgeaction.ErrDocumentNotFound) {
		t.Fatal("deleted preview", err)
	}
	if err := knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		record, err := fileaction.NewGetQuery(db).Execute(ctx, identity, id)
		if err != nil || record.Status != string(domain.FileStatusDeleting) || record.ExpiresAt == nil {
			t.Fatalf("released=%+v %v", record, err)
		}
	}
}

// TestKnowledgeDocumentBatchIsolation 验证十个文件限制、跨企业边界、事务回滚和并发重试。
func TestKnowledgeDocumentBatchIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	other, otherBase := newDocumentFixture(t, db)
	create := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db))
	first := uploadedDocumentFile(t, db, owner.Identity, "same.txt")
	foreign := uploadedDocumentFile(t, db, other.Identity, "foreign.txt")
	if _, err := create.Execute(ctx, owner.Identity, base.ID, []string{first.ID, foreign.ID}); !errors.Is(err, fileaction.ErrFileNotFound) {
		t.Fatal("foreign file", err)
	}
	record, _ := fileaction.NewGetQuery(db).Execute(ctx, owner.Identity, first.ID)
	if record.Status != string(domain.FileStatusUploaded) {
		t.Fatal("batch failed to roll back")
	}
	if _, err := create.Execute(ctx, owner.Identity, otherBase.ID, []string{first.ID}); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatal("foreign base", err)
	}
	for _, ids := range [][]string{nil, {first.ID, first.ID}, make([]string, 11)} {
		if _, err := create.Execute(ctx, owner.Identity, base.ID, ids); !errors.Is(err, knowledgeaction.ErrDocumentBatchInvalid) {
			t.Fatal("batch bound", err)
		}
	}
	var wait sync.WaitGroup
	results := make(chan []knowledgeaction.DocumentRecord, 2)
	failures := make(chan error, 2)
	for range 2 {
		wait.Go(func() {
			result, err := create.Execute(ctx, owner.Identity, base.ID, []string{first.ID})
			results <- result
			failures <- err
		})
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for result := range results {
		if id != "" && result[0].ID != id {
			t.Fatal("duplicate document")
		}
		id = result[0].ID
	}
	query := knowledgeaction.NewDocumentQuery(db)
	if _, err := query.Get(ctx, other.Identity, base.ID, id); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatal("foreign document", err)
	}
	if _, err := query.File(ctx, other.Identity, base.ID, id); !errors.Is(err, knowledgeaction.ErrDocumentNotFound) {
		t.Fatal("foreign preview", err)
	}
	if _, err := query.List(ctx, other.Identity, base.ID, knowledgeaction.DocumentListInput{}); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatal("foreign list", err)
	}
	if err := knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, other.Identity, base.ID, id); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatal("foreign delete", err)
	}
	sameOrgBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, owner.Identity, newKnowledgeBaseInput(t, db, owner.Identity, "其他资料", domain.KnowledgeBaseCategoryStandard))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.Get(ctx, owner.Identity, sameOrgBase.ID, id); !errors.Is(err, knowledgeaction.ErrDocumentNotFound) {
		t.Fatal("other base document", err)
	}
	if page, err := query.List(ctx, owner.Identity, sameOrgBase.ID, knowledgeaction.DocumentListInput{}); err != nil || page.Total != 0 {
		t.Fatalf("other base list=%+v %v", page, err)
	}
	if _, err := create.Execute(ctx, owner.Identity, sameOrgBase.ID, []string{first.ID}); !errors.Is(err, fileaction.ErrFileNotFound) {
		t.Fatal("other base retry", err)
	}
	ids := make([]string, 10)
	for i := range ids {
		ids[i] = uploadedDocumentFile(t, db, owner.Identity, "same.txt").ID
	}
	if result, err := create.Execute(ctx, owner.Identity, base.ID, ids); err != nil || len(result) != 10 {
		t.Fatal("ten files", err)
	}
}

// TestKnowledgeDocumentLocalPreview 验证原件只允许当前企业已登录成员读取，删除后失效。
func TestKnowledgeDocumentLocalPreview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	other, _ := newDocumentFixture(t, db)
	file := uploadedDocumentFile(t, db, owner.Identity, "preview.txt")
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{file.ID})
	if err != nil {
		t.Fatal(err)
	}
	local, err := filecontent.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := "preview text"
	if err := local.Save(ctx, file.StorageKey, strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	service := api.NewLocalObjectService(direct.NewLocalObjectAuthorizer(db), local)
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {other.Token, 401}, {owner.Token, 200}} {
		request := httptest.NewRequest(http.MethodGet, "/"+file.StorageKey, nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("preview status=%d want=%d", response.Code, test.status)
		}
		if test.status == 200 && (response.Body.String() != content || response.Header().Get("Cache-Control") != "private, no-store") {
			t.Fatal("preview content or cache")
		}
	}
	if err := knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, owner.Identity, base.ID, docs[0].ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/"+file.StorageKey, nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+owner.Token)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != 404 {
		t.Fatal("deleted raw file still readable")
	}
}

// TestKnowledgeDocumentS3Preview 验证停用 S3 后仍签发原件预览，删除阻止签发并交给对象清理。
func TestKnowledgeDocumentS3Preview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	// HTTP 测试端点记录客户端读取与清理请求。
	var mu sync.Mutex
	objects := map[string]bool{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodDelete {
			if r.Header.Get("Authorization") == "" {
				t.Error("unsigned cleanup")
			}
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !objects[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("X-Amz-Signature") == "" {
			t.Error("unsigned preview")
		}
		_, _ = io.WriteString(w, "S3 preview")
	}))
	defer endpoint.Close()
	s3 := filecontent.S3Config{Enabled: true, Endpoint: endpoint.URL, PublicBaseURL: endpoint.URL + "/app", Region: "us-east-1", Bucket: "app", AccessKeyID: "test-access", SecretAccessKey: "test-secret", ForcePathStyle: true}
	backend := direct.New(db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, s3, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Organization.ID, Locale: appservice.LocaleChineseSimplified}
	files := make([]*servermodels.File, 2)
	for i := range files {
		record, err := fileaction.NewCreateUploadAction(db).Execute(ctx, owner.Identity, domain.FileStorageBackendS3, fileaction.UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: "source.txt", ByteSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		files[i], err = markFileUploaded(ctx, db, owner.Identity, record.ID, "etag")
		if err != nil {
			t.Fatal(err)
		}
		objects["/app/"+record.StorageKey] = true
	}
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{files[0].ID, files[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	// 存储开关关闭时按文件记录中的存储类型签发预览并清理。
	s3.Enabled = false
	backend = direct.New(db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, s3, nil, nil, nil, nil, nil)
	cleanup := filemaintenance.NewDeleteExpiredAction(db, filecontent.NewDeleter(nil, s3))
	for i, doc := range docs {
		request, err := backend.GetKnowledgeDocumentPreview(ctx, meta, base.ID, doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := url.Parse(request.URL)
		if err != nil {
			t.Fatal(err)
		}
		if signed.Path != "/app/"+files[i].StorageKey || signed.Query().Get("response-content-disposition") != "inline" || len(request.Headers) != 0 {
			t.Fatal("invalid direct preview request")
		}
		response, err := http.Get(request.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != "S3 preview" {
			t.Fatal("preview failed", err)
		}
		if i == 0 {
			err = backend.DeleteKnowledgeDocument(ctx, meta, base.ID, doc.ID)
		} else {
			err = backend.DeleteKnowledgeBase(ctx, meta, base.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = backend.GetKnowledgeDocumentPreview(ctx, meta, base.ID, doc.ID); err == nil {
			t.Fatal("deleted document still signs preview")
		}
		if err = cleanup.Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: files[i].ID}); err != nil {
			t.Fatal(err)
		}
		response, err = http.Get(request.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("cleaned object still readable")
		}
		if _, err = fileaction.NewGetQuery(db).Execute(ctx, owner.Identity, files[i].ID); !errors.Is(err, fileaction.ErrFileNotFound) {
			t.Fatal("cleaned metadata retained", err)
		}
	}
}

// newKnowledgeTasks 创建可持久化文档与问答索引任务的测试运行时。
func newKnowledgeTasks(t *testing.T, db *bun.DB) *servertask.Runtime {
	t.Helper()
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(knowledgeaction.ProcessDocumentActionName, func(context.Context, knowledgeaction.ProcessInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Registry().RegisterJSON(knowledgeaction.ProcessQAEntryActionName, func(context.Context, knowledgeaction.ProcessQAInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return tasks
}
