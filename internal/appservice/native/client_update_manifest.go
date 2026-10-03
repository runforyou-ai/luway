//go:build !server && ((darwin && !ios) || windows || (linux && !android))

package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"golang.org/x/mod/semver"
)

// manifestClient 读取更新清单的版本信息，只限制单次请求时长。
var manifestClient = &http.Client{Timeout: 30 * time.Second}

// offeredUpdate 是当前服务器更新清单给出的客户端版本。
type offeredUpdate struct {
	// Version 是服务器提供的客户端版本，未连接服务器或服务器不提供客户端时为空。
	Version string
	// Installable 表示清单含本机平台的更新包。
	Installable bool
}

// NewerThan 判断服务器提供的版本是否高于 current。
func (o offeredUpdate) NewerThan(current string) bool {
	return semver.Compare("v"+o.Version, "v"+current) > 0
}

// readOfferedUpdate 读取当前服务器为本机平台给出的更新清单中的版本与是否含更新包，current 是本机客户端版本；不校验签名，只用于提示新版本。
func readOfferedUpdate(ctx context.Context, serverURL func(context.Context) (string, error), current string) (offeredUpdate, error) {
	address, err := serverURL(ctx)
	if err != nil || address == "" {
		return offeredUpdate{}, err
	}
	query := url.Values{"platform": {runtime.GOOS}, "arch": {runtime.GOARCH}, "version": {current}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(address, "/")+clientrelease.UpdatePath+"?"+query.Encode(), nil)
	if err != nil {
		return offeredUpdate{}, err
	}
	response, err := manifestClient.Do(request)
	if err != nil {
		return offeredUpdate{}, err
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotFound:
		return offeredUpdate{}, nil
	case response.StatusCode != http.StatusOK:
		return offeredUpdate{}, fmt.Errorf("更新清单请求失败: HTTP %d", response.StatusCode)
	}
	var manifest struct {
		Version   string            `json:"version"`
		Artifacts []json.RawMessage `json:"artifacts"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&manifest); err != nil {
		return offeredUpdate{}, fmt.Errorf("解析更新清单: %w", err)
	}
	return offeredUpdate{Version: manifest.Version, Installable: len(manifest.Artifacts) > 0}, nil
}
