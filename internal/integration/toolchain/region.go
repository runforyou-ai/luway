package toolchain

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// regionTimeout 是探测公网出口所在地区的时限。
const regionTimeout = 5 * time.Second

// detectSources 按本机公网出口所在地区选择下载源：位于中国大陆或无法探测时使用国内镜像，其他地区使用官方源；detected 表示探测是否成功。
func detectSources(ctx context.Context, client *http.Client, traceURL string) (sources Sources, detected bool) {
	ctx, cancel := context.WithTimeout(ctx, regionTimeout)
	defer cancel()
	response, err := get(ctx, client, traceURL, "text/plain")
	if err != nil {
		slog.Info("无法探测公网出口所在地区，运行环境使用国内镜像", "error", err)
		return chinaSources, false
	}
	defer response.Body.Close()
	country := ""
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "loc="); ok {
			country = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Info("无法读取公网出口所在地区，运行环境使用国内镜像", "error", err)
		return chinaSources, false
	}
	slog.Info("已按公网出口所在地区选择运行环境下载源", "country", country)
	if country == "" || country == "CN" {
		return chinaSources, true
	}
	return Sources{}, true
}
