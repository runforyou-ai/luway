//go:build server

package integrationtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	filecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// newDocumentFixture 创建独立工作区和标准知识库。
func newDocumentFixture(t *testing.T, db *bun.DB) (servertest.InstalledWorkspace, *knowledgeaction.Record) {
	t.Helper()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "文档测试", DisplayName: "维护人员", Email: servertest.UniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(context.Background(), installed.Identity, newKnowledgeBaseInput(t, db, installed.Identity, "资料", domain.KnowledgeBaseCategoryStandard))
	require.NoError(t, err)
	return installed, base
}

// uploadedDocumentFile 创建已核验上传的测试原件。
func uploadedDocumentFile(t *testing.T, db *bun.DB, identity *servermodels.Identity, name string) *servermodels.File {
	t.Helper()
	ctx := context.Background()
	record, err := fileaction.NewCreateUploadAction(db).Execute(ctx, identity, domain.FileStorageBackendLocal, fileaction.UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: name, ContentType: "application/octet-stream", ByteSize: 12})
	require.NoError(t, err)
	record, err = markFileUploaded(ctx, db, identity, record.ID, "etag")
	require.NoError(t, err)
	return record
}

// TestKnowledgeDocumentLifecycle 验证批次幂等、知识库内倒序分页及删除原件状态。
func TestKnowledgeDocumentLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	first := uploadedDocumentFile(t, db, identity, "报表100%.XLSX")
	second := uploadedDocumentFile(t, db, identity, "说明.pdf")
	create := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db))
	query := knowledgeaction.NewDocumentQuery(db)
	docs, err := create.Execute(ctx, identity, base.ID, []string{first.ID, second.ID})
	require.NoError(t, err)
	require.Len(t, docs, 2)
	repeat, err := create.Execute(ctx, identity, base.ID, []string{first.ID, second.ID})
	require.NoError(t, err)
	require.Equal(t, docs[0].ID, repeat[0].ID)
	page, err := query.List(ctx, identity, base.ID, knowledgeaction.DocumentListInput{PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	require.Equal(t, docs[1].ID, page.Documents[0].ID)
	for keyword, count := range map[string]int{"100%": 1, "_": 0, "报表": 1} {
		result, err := query.List(ctx, identity, base.ID, knowledgeaction.DocumentListInput{Keyword: keyword})
		require.NoError(t, err, "search %s", keyword)
		require.Equal(t, count, result.Total, "search %s", keyword)
	}
	require.Equal(t, domain.KnowledgeIndexQueued, docs[0].Status, "new document not queued")
	detail, err := query.Get(ctx, identity, base.ID, docs[0].ID)
	require.NoError(t, err)
	require.Equal(t, docs[0].ID, detail.ID)
	require.Equal(t, docs[0].Name, detail.Name)
	_, err = knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, newKnowledgeBaseInput(t, db, identity, base.Name, domain.KnowledgeBaseCategoryQA))
	require.ErrorIs(t, err, knowledgeaction.ErrBaseHasContent, "occupied base")
	require.NoError(t, knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, identity, base.ID, docs[0].ID))
	_, err = query.File(ctx, identity, base.ID, docs[0].ID)
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentNotFound, "deleted preview")
	require.NoError(t, knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID))
	for _, id := range []string{first.ID, second.ID} {
		record, err := fileaction.NewGetQuery(db).Execute(ctx, identity, id)
		require.NoError(t, err)
		require.Equal(t, string(domain.FileStatusDeleting), record.Status)
		require.NotNil(t, record.ExpiresAt)
	}
}

// TestKnowledgeDocumentBatchIsolation 验证十个文件限制、跨企业边界、事务回滚和并发重试。
func TestKnowledgeDocumentBatchIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	other, otherBase := newDocumentFixture(t, db)
	create := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db))
	first := uploadedDocumentFile(t, db, owner.Identity, "same.txt")
	foreign := uploadedDocumentFile(t, db, other.Identity, "foreign.txt")
	_, err = create.Execute(ctx, owner.Identity, base.ID, []string{first.ID, foreign.ID})
	require.ErrorIs(t, err, fileaction.ErrFileNotFound, "foreign file")
	record, _ := fileaction.NewGetQuery(db).Execute(ctx, owner.Identity, first.ID)
	require.Equal(t, string(domain.FileStatusUploaded), record.Status, "batch failed to roll back")
	_, err = create.Execute(ctx, owner.Identity, otherBase.ID, []string{first.ID})
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign base")
	for _, ids := range [][]string{nil, {first.ID, first.ID}, make([]string, 11)} {
		_, err := create.Execute(ctx, owner.Identity, base.ID, ids)
		require.ErrorIs(t, err, knowledgeaction.ErrDocumentBatchInvalid, "batch bound")
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
		require.NoError(t, err)
	}
	id := ""
	for result := range results {
		if id != "" {
			require.Equal(t, id, result[0].ID, "duplicate document")
		}
		id = result[0].ID
	}
	query := knowledgeaction.NewDocumentQuery(db)
	_, err = query.Get(ctx, other.Identity, base.ID, id)
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign document")
	_, err = query.File(ctx, other.Identity, base.ID, id)
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentNotFound, "foreign preview")
	_, err = query.List(ctx, other.Identity, base.ID, knowledgeaction.DocumentListInput{})
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign list")
	require.ErrorIs(t, knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, other.Identity, base.ID, id), knowledgeaction.ErrNotFound, "foreign delete")
	sameOrgBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, owner.Identity, newKnowledgeBaseInput(t, db, owner.Identity, "其他资料", domain.KnowledgeBaseCategoryStandard))
	require.NoError(t, err)
	_, err = query.Get(ctx, owner.Identity, sameOrgBase.ID, id)
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentNotFound, "other base document")
	page, err := query.List(ctx, owner.Identity, sameOrgBase.ID, knowledgeaction.DocumentListInput{})
	require.NoError(t, err)
	require.Zero(t, page.Total, "other base list")
	_, err = create.Execute(ctx, owner.Identity, sameOrgBase.ID, []string{first.ID})
	require.ErrorIs(t, err, fileaction.ErrFileNotFound, "other base retry")
	ids := make([]string, 10)
	for i := range ids {
		ids[i] = uploadedDocumentFile(t, db, owner.Identity, "same.txt").ID
	}
	result, err := create.Execute(ctx, owner.Identity, base.ID, ids)
	require.NoError(t, err, "ten files")
	require.Len(t, result, 10, "ten files")
}

// TestKnowledgeDocumentLocalPreview 验证原件只允许当前企业已登录成员读取，删除后失效。
func TestKnowledgeDocumentLocalPreview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	other, _ := newDocumentFixture(t, db)
	file := uploadedDocumentFile(t, db, owner.Identity, "preview.txt")
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{file.ID})
	require.NoError(t, err)
	local, err := filecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	content := "preview text"
	require.NoError(t, local.Save(ctx, file.StorageKey, strings.NewReader(content), int64(len(content))))
	service := api.NewLocalObjectService(direct.NewLocalObjectAuthorizer(db), local)
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {other.Token, 401}, {owner.Token, 200}} {
		request := httptest.NewRequest(http.MethodGet, "/"+file.StorageKey, nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		require.Equal(t, test.status, response.Code, "preview status")
		if test.status == 200 {
			require.Equal(t, content, response.Body.String(), "preview content")
			require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "preview cache")
		}
	}
	require.NoError(t, knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, owner.Identity, base.ID, docs[0].ID))
	request := httptest.NewRequest(http.MethodGet, "/"+file.StorageKey, nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+owner.Token)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	require.Equal(t, 404, response.Code, "deleted raw file still readable")
}

// TestKnowledgeDocumentS3Preview 验证停用 S3 后仍签发原件预览，删除阻止签发并交给对象清理。
func TestKnowledgeDocumentS3Preview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	owner, base := newDocumentFixture(t, db)
	// HTTP 测试端点记录客户端读取与清理请求。
	var mu sync.Mutex
	objects := map[string]bool{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodDelete {
			assert.NotEmpty(t, r.Header.Get("Authorization"), "unsigned cleanup")
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !objects[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		assert.NotEmpty(t, r.URL.Query().Get("X-Amz-Signature"), "unsigned preview")
		_, _ = io.WriteString(w, "S3 preview")
	}))
	defer endpoint.Close()
	_, err := db.NewRaw(`UPDATE platforms SET s3_enabled = true, s3_endpoint = ?, s3_public_base_url = ?, s3_region = 'us-east-1', s3_bucket = 'app',
		s3_access_key_id = 'test-access', s3_secret_access_key = 'test-secret', s3_force_path_style = true`, endpoint.URL, endpoint.URL+"/app").Exec(ctx)
	require.NoError(t, err)
	deployment := servertest.TestDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment}, nil, nil, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	files := make([]*servermodels.File, 2)
	for i := range files {
		record, err := fileaction.NewCreateUploadAction(db).Execute(ctx, owner.Identity, domain.FileStorageBackendS3, fileaction.UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: "source.txt", ByteSize: 10})
		require.NoError(t, err)
		files[i], err = markFileUploaded(ctx, db, owner.Identity, record.ID, "etag")
		require.NoError(t, err)
		objects["/app/"+record.StorageKey] = true
	}
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{files[0].ID, files[1].ID})
	require.NoError(t, err)
	// 存储开关关闭时按文件记录中的存储类型签发预览并清理。
	_, err = db.NewRaw("UPDATE platforms SET s3_enabled = false").Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, deployment.Reload(ctx))
	cleanup := filemaintenance.NewDeleteExpiredAction(db, filecontent.NewDeleter(nil, deployment.S3))
	for i, doc := range docs {
		request, err := backend.GetKnowledgeDocumentPreview(ctx, meta, base.ID, doc.ID)
		require.NoError(t, err)
		signed, err := url.Parse(request.URL)
		require.NoError(t, err)
		require.Equal(t, "/app/"+files[i].StorageKey, signed.Path, "invalid direct preview request")
		require.Equal(t, "inline", signed.Query().Get("response-content-disposition"), "invalid direct preview request")
		require.Empty(t, request.Headers, "invalid direct preview request")
		response, err := http.Get(request.URL)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, err, "preview failed")
		require.Equal(t, 200, response.StatusCode, "preview failed")
		require.Equal(t, "S3 preview", string(body), "preview failed")
		if i == 0 {
			err = backend.DeleteKnowledgeDocument(ctx, meta, base.ID, doc.ID)
		} else {
			err = backend.DeleteKnowledgeBase(ctx, meta, base.ID)
		}
		require.NoError(t, err)
		_, err = backend.GetKnowledgeDocumentPreview(ctx, meta, base.ID, doc.ID)
		require.Error(t, err, "deleted document still signs preview")
		require.NoError(t, cleanup.Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: files[i].ID}))
		response, err = http.Get(request.URL)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, 404, response.StatusCode, "cleaned object still readable")
		_, err = fileaction.NewGetQuery(db).Execute(ctx, owner.Identity, files[i].ID)
		require.ErrorIs(t, err, fileaction.ErrFileNotFound, "cleaned metadata retained")
	}
}

// newKnowledgeTasks 创建记录文档与问答索引任务的任务登记器。
func newKnowledgeTasks(t *testing.T, db *bun.DB) *servertest.Tasks {
	t.Helper()
	return testEnqueuer
}
