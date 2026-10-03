//go:build server

package api

import (
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
)

// LocalObjectService 通过稳定对象键处理本地文件的上传和静态读取，认证与归属校验由 appservice 完成。
type LocalObjectService struct {
	authorizer *direct.LocalObjectAuthorizer
	local      *serverfilecontent.LocalStore
	objects    http.Handler
}

// NewLocalObjectService 创建本地对象服务。
func NewLocalObjectService(authorizer *direct.LocalObjectAuthorizer, local *serverfilecontent.LocalStore) *LocalObjectService {
	return &LocalObjectService{authorizer: authorizer, local: local, objects: http.FileServerFS(local.ObjectsFS())}
}

// ServeHTTP 处理本地对象请求。
func (s *LocalObjectService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// 允许原生端 WebView 直传和读取企业服务器对象。
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+websiteVisitorHeader+", "+websiteCustomerHeader)
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	storageKey, ok := localObjectStorageKey(request.URL.Path)
	if !ok {
		writeLocalObjectError(writer, direct.ErrLocalObjectNotFound)
		return
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		if strings.Split(storageKey, "/")[2] == "knowledge-documents" {
			s.previewKnowledgeObject(writer, request, storageKey)
			return
		}
		// 可内嵌展示的图片按扩展名输出图片类型，其余文件一律按附件下载，响应在沙箱中打开且不推断类型。
		contentType, inline := domain.InlineImageContentType(path.Ext(storageKey))
		if !inline {
			contentType = "application/octet-stream"
		}
		writer.Header().Set("Content-Type", contentType)
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "sandbox")
		if name := request.URL.Query().Get("download"); name != "" {
			writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		} else if !inline {
			writer.Header().Set("Content-Disposition", "attachment")
		}
		// 通过最终对象目录的静态文件服务输出不可变文件。
		s.objects.ServeHTTP(&localObjectResponseWriter{ResponseWriter: writer}, request)
	case http.MethodPut:
		s.uploadLocalObject(writer, request, storageKey)
	default:
		writer.Header().Set("Allow", "GET, HEAD, PUT, OPTIONS")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// uploadLocalObject 授权后将请求内容保存到本地最终对象目录，分片文件写入对应分片。
func (s *LocalObjectService) uploadLocalObject(writer http.ResponseWriter, request *http.Request, storageKey string) {
	upload, err := s.authorizer.AuthorizeUpload(request.Context(), direct.LocalObjectCredentials{
		Bearer:        appservice.BearerToken(request.Header.Get("Authorization")),
		VisitorToken:  strings.TrimSpace(request.Header.Get(websiteVisitorHeader)),
		CustomerToken: strings.TrimSpace(request.Header.Get(websiteCustomerHeader)),
	}, storageKey, request.URL.Query().Get("partNumber"))
	if err != nil {
		writeLocalObjectError(writer, err)
		return
	}
	if request.ContentLength >= 0 && request.ContentLength != upload.ExpectedSize {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if upload.PartNumber > 0 {
		err = s.local.SavePart(request.Context(), storageKey, upload.PartNumber, request.Body, upload.ExpectedSize)
	} else {
		err = s.local.Save(request.Context(), storageKey, request.Body, upload.ExpectedSize)
	}
	if err != nil {
		slog.Warn("本地文件写入失败", "file_id", upload.FileID, "error", err)
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// writeLocalObjectError 把本地对象授权错误映射为 HTTP 状态码。
func writeLocalObjectError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, direct.ErrLocalObjectUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, direct.ErrLocalObjectNotFound):
		status = http.StatusNotFound
	case errors.Is(err, direct.ErrLocalObjectConflict):
		status = http.StatusConflict
	case errors.Is(err, direct.ErrLocalObjectInvalid):
		status = http.StatusBadRequest
	default:
		slog.Warn("本地对象授权失败", "error", err)
	}
	http.Error(writer, http.StatusText(status), status)
}

// localObjectResponseWriter 只为已命中的静态对象添加不可变缓存策略。
type localObjectResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

// WriteHeader 根据静态文件服务的最终状态写入缓存策略。
func (w *localObjectResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if (status >= http.StatusOK && status < http.StatusMultipleChoices) || status == http.StatusNotModified {
		w.Header().Set("Cache-Control", serverfilecontent.ImmutableCacheControl)
	} else {
		w.Header().Del("Cache-Control")
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write 确保隐式成功响应同样带上不可变缓存策略。
func (w *localObjectResponseWriter) Write(content []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(content)
}

// localObjectStorageKey 从公开路径中读取规范对象键。
func localObjectStorageKey(requestPath string) (string, bool) {
	storageKey := strings.TrimPrefix(requestPath, "/")
	parts := strings.Split(storageKey, "/")
	if len(parts) != 4 || parts[0] != "organizations" || (parts[2] != "files" && parts[2] != "knowledge-documents") || !common.ValidUUID(parts[1]) {
		return "", false
	}
	extension := path.Ext(parts[3])
	if extension == "" || !common.ValidUUID(strings.TrimSuffix(parts[3], extension)) {
		return "", false
	}
	return storageKey, true
}

// previewKnowledgeObject 授权后读取仍在使用的知识文档原件，禁止共享缓存。
func (s *LocalObjectService) previewKnowledgeObject(writer http.ResponseWriter, request *http.Request, storageKey string) {
	name, err := s.authorizer.AuthorizeKnowledgePreview(request.Context(), appservice.BearerToken(request.Header.Get("Authorization")), storageKey)
	if err != nil {
		writeLocalObjectError(writer, err)
		return
	}
	file, info, err := s.local.Open(request.Context(), storageKey)
	if err != nil {
		writeLocalObjectError(writer, direct.ErrLocalObjectNotFound)
		return
	}
	defer file.Close()
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(writer, request, name, info.ModTime(), file)
}
