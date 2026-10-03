//go:build !server && (ios || android)

package main

import (
	"github.com/runforyou-ai/luway/internal/apiproxy"
	"github.com/runforyou-ai/luway/internal/clientsession"
)

// nativeStorage 组合移动端连接和登录凭据存储能力。
type nativeStorage interface {
	apiproxy.Store
	clientsession.Store
}

// newLocalComputer 返回空电脑，移动端不作为电脑执行操作。
func newLocalComputer(nativeStorage, *apiproxy.Backend, *clientsession.Manager) localComputer {
	return nil
}
