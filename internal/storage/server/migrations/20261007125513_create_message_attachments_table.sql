-- +goose Up
-- 创建附件消息关联的文件表。
CREATE TABLE message_attachments (
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    message_id       uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL,
    file_id          uuid,
    name             text NOT NULL,
    content_type     text NOT NULL,
    byte_size        bigint NOT NULL,
    image_width      integer NOT NULL DEFAULT 0,
    image_height     integer NOT NULL DEFAULT 0,
    transfer_status  text NOT NULL
);

CREATE UNIQUE INDEX message_attachments_file_id_unique
    ON message_attachments (file_id);

CREATE TRIGGER message_attachments_set_updated_at BEFORE UPDATE ON message_attachments FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE message_attachments IS '附件消息关联的文件';
COMMENT ON COLUMN message_attachments.created_at IS '创建时间';
COMMENT ON COLUMN message_attachments.updated_at IS '更新时间';
COMMENT ON COLUMN message_attachments.message_id IS '消息编号';
COMMENT ON COLUMN message_attachments.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN message_attachments.file_id IS '附件文件编号，过期清理后为空';
COMMENT ON COLUMN message_attachments.name IS '原始文件名';
COMMENT ON COLUMN message_attachments.content_type IS '内容类型';
COMMENT ON COLUMN message_attachments.byte_size IS '文件字节数';
COMMENT ON COLUMN message_attachments.image_width IS '图片宽度，非图片为 0';
COMMENT ON COLUMN message_attachments.image_height IS '图片高度，非图片为 0';
COMMENT ON COLUMN message_attachments.transfer_status IS '附件内容取回状态：ready 已就绪、pending 取回中、failed 取回失败';
COMMENT ON INDEX message_attachments_file_id_unique IS '一个上传文件仅关联一条消息';

-- +goose Down
DROP TABLE message_attachments;
