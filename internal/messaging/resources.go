//go:build server

package messaging

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/runforyou-ai/jetq"
)

// 内部消息资源名称在每个部署专用的 NATS 账户内固定。
const (
	RealtimeStream      = "APP_REALTIME"
	RealtimeInput       = "app.rt.in."
	RealtimeEvents      = "app.rt.ev."
	RealtimeLive        = "app.rt.live."
	RealtimeRevoke      = "sys.revoke"
	BusPrefix           = "app.bus"
	RunPrefix           = "app.runs"
	HistoryAge          = 5 * time.Minute
	DuplicateWindow     = 2 * time.Hour
	JobStateTTL         = 48 * time.Hour
	JobFailureRetention = 7 * 24 * time.Hour
)

// QueueOptions 返回任务资源的逻辑契约。
func QueueOptions() []jetq.Option {
	return []jetq.Option{jetq.WithStreamName("APP_JOBS"), jetq.WithSubjectPrefix("app-jobs"), jetq.WithDuplicateWindow(DuplicateWindow), jetq.WithDeadLetterMaxAge(JobFailureRetention), jetq.WithUniqueLockTTL(JobStateTTL)}
}

// EventConfig 返回带发布确认后转发的实时历史流定义。
func EventConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{Name: RealtimeStream, Subjects: []string{RealtimeInput + ">"}, Retention: jetstream.LimitsPolicy, Storage: jetstream.FileStorage, Replicas: 1, MaxAge: HistoryAge, MaxBytes: 1 << 30, Discard: jetstream.DiscardOld, DenyDelete: true, DenyPurge: true, Duplicates: HistoryAge, RePublish: &jetstream.RePublish{Source: RealtimeInput + ">", Destination: RealtimeEvents + ">"}}
}

// Provision 初始化消息资源，已存在资源保留容量、放置和存储副本策略。
func Provision(ctx context.Context, js jetstream.JetStream, replicas int, storage jetstream.StorageType) error {
	if replicas < 1 || replicas > 5 {
		return errors.New("replicas must be between 1 and 5")
	}
	opts := append(QueueOptions(), jetq.WithReplicas(replicas))
	if storage == jetstream.MemoryStorage {
		opts = append(opts, jetq.WithMemoryStorage())
	}
	resources, err := jetq.ResourceConfigs(opts...)
	if err != nil {
		return err
	}
	event := EventConfig()
	event.Replicas = replicas
	event.Storage = storage
	for _, want := range []jetstream.StreamConfig{resources.Work, resources.Dead, event} {
		existing, err := js.Stream(ctx, want.Name)
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			_, err = js.CreateStream(ctx, want)
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		info, err := existing.Info(ctx)
		if err != nil {
			return err
		}
		if info.Config.Replicas != replicas || info.Config.Storage != storage {
			return fmt.Errorf("resource %s has a different deployment policy; manage replicas and storage explicitly", want.Name)
		}
		got := info.Config
		got.Subjects, got.Retention, got.AllowMsgSchedules, got.AllowRollup = want.Subjects, want.Retention, want.AllowMsgSchedules, want.AllowRollup
		got.MaxAge, got.Duplicates, got.RePublish = want.MaxAge, want.Duplicates, want.RePublish
		got.DenyDelete, got.DenyPurge = want.DenyDelete, want.DenyPurge
		if _, err = js.UpdateStream(ctx, got); err != nil {
			return err
		}
	}
	kv, err := js.KeyValue(ctx, resources.State.Bucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		_, err = js.CreateKeyValue(ctx, resources.State)
	} else if err == nil {
		status, statusErr := kv.Status(ctx)
		if statusErr != nil {
			return statusErr
		}
		if status.TTL() != 0 && status.TTL() < JobStateTTL {
			return errors.New("task state TTL must be at least 48 hours")
		}
	}
	if err != nil {
		return err
	}
	stateStream, err := js.Stream(ctx, "KV_"+resources.State.Bucket)
	if err != nil {
		return err
	}
	if c := stateStream.CachedInfo().Config; c.Replicas != replicas || c.Storage != storage {
		return fmt.Errorf("resource %s has a different deployment policy", c.Name)
	}
	return Validate(ctx, js)
}

// Validate 校验任务和实时资源，只读取现有配置。
func Validate(ctx context.Context, js jetstream.JetStream) error {
	if _, err := jetq.Attach(ctx, js, QueueOptions()...); err != nil {
		return fmt.Errorf("NATS resources require nats-init: %w", err)
	}
	stream, err := js.Stream(ctx, RealtimeStream)
	if err != nil {
		return fmt.Errorf("NATS realtime requires nats-init: %w", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return err
	}
	c, w := info.Config, EventConfig()
	if !slices.Equal(c.Subjects, w.Subjects) || c.Retention != w.Retention || c.Sealed || !c.DenyDelete || !c.DenyPurge || c.AllowRollup || c.MaxMsgsPerSubject > 0 || c.Discard != jetstream.DiscardOld || c.Mirror != nil || len(c.Sources) > 0 || c.SubjectTransform != nil || c.Duplicates < HistoryAge || c.MaxAge != HistoryAge || c.RePublish == nil || *c.RePublish != *w.RePublish {
		return errors.New("NATS realtime resources require nats-init")
	}
	return nil
}
