//go:build server

package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/messaging"
)

// natsInitCommand 为外部消息服务创建或升级应用资源。
func natsInitCommand(arguments []string) error {
	flags := newFlags("nats-init", "")
	replicas := flags.Int("replicas", 1, "新资源副本数，已有资源必须与此一致")
	storage := flags.String("storage", "file", "存储类型 file 或 memory")
	if err := parseFlags(flags, arguments, 0); err != nil {
		return err
	}
	config, err := serverconfig.Load()
	if err != nil {
		return err
	}
	if config.NATS.URL == "" {
		return errors.New("nats-init requires external nats.url")
	}
	kind := jetstream.FileStorage
	if *storage == "memory" {
		kind = jetstream.MemoryStorage
	} else if *storage != "file" {
		return errors.New("storage must be file or memory")
	}
	nc, err := nats.Connect(config.NATS.URL, nats.Name("resource-initializer"))
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = messaging.Provision(ctx, js, *replicas, kind); err != nil {
		return err
	}
	fmt.Println("NATS 资源已就绪")
	return nil
}
