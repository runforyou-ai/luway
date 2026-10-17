//go:build server

package integrationtest

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
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
	record, err := fileaction.NewCreateUploadAction(db).Execute(ctx, identity, fileaction.UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: name, ContentType: "application/octet-stream", ByteSize: 12})
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

// TestKnowledgeDocumentS3Preview 验证知识原件使用稳定公开地址，删除后清理对象。
func TestKnowledgeDocumentS3Preview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	owner, base := newDocumentFixture(t, db)
	storage := servertest.NewFiles(t)
	storage.Configure(t, db)
	deployment := servertest.TestDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment}, nil, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	files := make([]*servermodels.File, 2)
	for i := range files {
		upload, err := backend.CreateFileUpload(ctx, meta, appservice.FileUploadInput{Purpose: appservice.FilePurposeKnowledgeDocument, FileName: "source.txt", ContentType: "text/plain", ByteSize: 10})
		require.NoError(t, err)
		require.Equal(t, storage.Config.PublicBaseURL+"/"+upload.Key, upload.File.ContentURL, "知识原件应暴露稳定公开地址")
		performS3Request(t, upload.Request, []byte("S3 preview"))
		completed, err := backend.CompleteFileUpload(ctx, meta, upload.File.ID)
		require.NoError(t, err)
		require.Equal(t, storage.Config.PublicBaseURL+"/"+upload.Key, completed.ContentURL, "知识原件完成上传后应保留公开地址")
		files[i] = &servermodels.File{ID: upload.File.ID, StorageKey: upload.Key}
	}
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{files[0].ID, files[1].ID})
	require.NoError(t, err)
	require.NoError(t, deployment.Reload(ctx))
	cleanup := filemaintenance.NewDeleteExpiredAction(db, filecontent.NewStore(deployment.S3))
	other := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "其他工作区", DisplayName: "其他成员", Email: servertest.UniqueEmail("private-preview"), Password: "password123"})
	for i, doc := range docs {
		for _, denied := range []appservice.RequestMeta{{}, {Token: other.Token, WorkspaceID: other.Identity.Workspace.ID}} {
			_, err := backend.GetKnowledgeDocumentPreview(ctx, denied, base.ID, doc.ID)
			require.Error(t, err, "未授权用户不能获得预览地址")
		}
		request, err := backend.GetKnowledgeDocumentPreview(ctx, meta, base.ID, doc.ID)
		require.NoError(t, err)
		require.Equal(t, storage.Config.PublicBaseURL+"/"+files[i].StorageKey, request.URL, "preview should be the stable public URL")
		require.Empty(t, request.Headers, "stable public preview has no signed headers")
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
		require.Error(t, err, "deleted document still returns preview")
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
