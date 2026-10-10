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

// response 是探针响应体。
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

// Readiness 提供检查 PostgreSQL 与消息总线连接的服务就绪探针。
type Readiness struct {
	db        *bun.DB
	connected []func() bool
}

// NewReadiness 创建服务就绪探针，connected 返回各项连接当前是否可用。
func NewReadiness(db *bun.DB, connected ...func() bool) *Readiness {
	return &Readiness{db: db, connected: connected}
}

// ServeHTTP 检查各项连接与 PostgreSQL 后返回服务就绪状态。
func (s *Readiness) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	for _, connected := range s.connected {
		if !connected() {
			writeResponse(writer, request, http.StatusServiceUnavailable, response{Status: "unavailable"})
			return
		}
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
