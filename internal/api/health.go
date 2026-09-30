//go:build server

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/uptrace/bun"
)

const readinessTimeout = 3 * time.Second

type response struct {
	Status string `json:"status"`
}

// Liveness 提供进程存活探针。
type Liveness struct{}

// NewLiveness 创建进程存活探针。
func NewLiveness() *Liveness {
	return &Liveness{}
}

// ServeHTTP 返回进程存活状态。
func (s *Liveness) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	writeResponse(writer, request, http.StatusOK, response{Status: "ok"})
}

// Readiness 提供依赖 PostgreSQL 的服务就绪探针。
type Readiness struct {
	db *bun.DB
}

// NewReadiness 创建服务就绪探针。
func NewReadiness(db *bun.DB) *Readiness {
	return &Readiness{db: db}
}

// ServeHTTP 检查 PostgreSQL 后返回服务就绪状态。
func (s *Readiness) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, "SELECT 1"); err != nil {
		writeResponse(writer, request, http.StatusServiceUnavailable, response{Status: "unavailable"})
		return
	}
	writeResponse(writer, request, http.StatusOK, response{Status: "ready"})
}

// writeResponse 输出统一的探针响应。
func writeResponse(writer http.ResponseWriter, request *http.Request, status int, body response) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(writer).Encode(body)
}
