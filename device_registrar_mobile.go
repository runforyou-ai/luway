//go:build !server && (ios || android)

package main

import (
	"github.com/runforyou-ai/cervi/internal/apiproxy"
	"github.com/runforyou-ai/cervi/internal/clientsession"
)

// nativeStorage 组合移动端连接和登录凭据存储能力。
type nativeStorage interface {
	apiproxy.Store
	clientsession.Store
}

// newDeviceRegistrar 返回空注册器，移动端不把本机注册为企业设备。
func newDeviceRegistrar(nativeStorage, *apiproxy.Backend, *clientsession.Manager) deviceRegistrar {
	return nil
}
