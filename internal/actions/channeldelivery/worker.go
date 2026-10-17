//go:build server

package channeldelivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// leaseMargin 是认领租约在发送超时之外保留的结果保存时间。
const leaseMargin = 25 * time.Second

// uncertaintyWindow 是认领过期后等待平台结果的时长，到期转为待人工确认。
const uncertaintyWindow = 30 * time.Second

// Worker 按渠道身份管道逐个请求项投递消息并恢复中断的发送。
type Worker struct {
	db       *bun.DB
	adapters *channeladapter.Registry
	files    filecontent.Opener
	enqueuer servertask.TxEnqueuer
}

// claimedDelivery 保存认领成功后本次发送的管道、投递、请求项、平台目标与内容。
type claimedDelivery struct {
	pipeline pipeline
	delivery *models.ChannelMessageDelivery
	item     *models.ChannelDeliveryItem
	outbound channeladapter.Outbound
	timeout  time.Duration
	target   channeladapter.Target
	request  channeladapter.Request
	// attachment 在附件请求项上有值，附件记录缺失或文件已清理时 file 为空。
	attachment *claimedAttachment
}

// claimedAttachment 保存附件请求项的展示元数据与存储位置。
type claimedAttachment struct {
	meta channeladapter.Attachment
	file *models.File
}

// NewWorker 创建持久投递执行器。
func NewWorker(db *bun.DB, adapters *channeladapter.Registry, files filecontent.Opener, enqueuer servertask.TxEnqueuer) *Worker {
	return &Worker{db: db, adapters: adapters, files: files, enqueuer: enqueuer}
}

// Advance 推进渠道身份的投递管道：依次处理已到期的队头，队头需要等待时在到期时刻排下一次推进；认领到请求项时在短事务之外调用平台并保存结果，
// 每次最多调用一次平台，定时推进不调用平台。同一渠道身份按位置串行发送，不同渠道身份并行发送。
func (w *Worker) Advance(ctx context.Context, input AdvanceInput) error {
	claimed, err := w.claim(ctx, input)
	if err != nil || claimed == nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, claimed.timeout)
	// 附件记录缺失、文件已清理或存储读取失败时平台未收到请求，投递直接失败并可人工重试。
	result := channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "attachment_unavailable"}
	if attachment := claimed.attachment; attachment == nil {
		result = claimed.outbound.Send(sendCtx, claimed.target, claimed.request)
	} else if attachment.file != nil {
		content, openErr := w.files.Open(sendCtx, attachment.file)
		if openErr != nil {
			slog.WarnContext(ctx, "渠道消息附件内容读取失败", "delivery_id", claimed.delivery.ID, "file_id", attachment.file.ID, "error", openErr)
		} else {
			file := attachment.meta
			file.Content = content
			claimed.request.File = &file
			result = claimed.outbound.Send(sendCtx, claimed.target, claimed.request)
			content.Close()
		}
	}
	cancel()
	// 请求结束或服务关闭后使用独立上下文保存平台结果。
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	return w.finish(saveCtx, claimed, result)
}

// claim 锁定渠道身份后依次处理队头：已结束的队头继续处理下一个，需要等待的队头在同一事务中按到期时刻排下一次推进，可发送的队头认领其下一个请求项；
