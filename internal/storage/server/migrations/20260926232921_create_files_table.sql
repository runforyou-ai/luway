-- +goose Up
-- 创建工作区文件元数据表。
CREATE TABLE files (
    id                            uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                    timestamptz NOT NULL DEFAULT now(),
    updated_at                    timestamptz NOT NULL DEFAULT now(),
    organization_id               uuid NOT NULL,
    created_by_user_id            uuid NOT NULL,
    uploader_channel_identity_id  uuid,
    purpose                       text NOT NULL,
    external_id                   text,
    storage_backend               text NOT NULL,
    storage_key                   text NOT NULL,
    original_name                 text NOT NULL,
    content_type                  text NOT NULL,
    byte_size                     bigint NOT NULL,
    status                        text NOT NULL DEFAULT 'pending',
    etag                          text,
    uploaded_at                   timestamptz,
    expires_at                    timestamptz,
    multipart_upload_id           text,
    part_size                     bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX files_storage_key_unique
    ON files (storage_key);

COMMENT ON TABLE files IS '工作区上传文件元数据';
COMMENT ON COLUMN files.id IS '文件编号';
COMMENT ON COLUMN files.created_at IS '创建时间';
COMMENT ON COLUMN files.updated_at IS '更新时间';
COMMENT ON COLUMN files.organization_id IS '所属工作区编号';
COMMENT ON COLUMN files.created_by_user_id IS '上传用户编号';
COMMENT ON COLUMN files.uploader_channel_identity_id IS '上传该文件的渠道访客身份编号，成员上传为空';
COMMENT ON COLUMN files.purpose IS '文件用途：user_avatar 用户头像、contact_avatar 联系人头像、group_image 群图片、message_attachment 消息附件';
COMMENT ON COLUMN files.external_id IS '外部来源的文件唯一标识';
COMMENT ON COLUMN files.storage_backend IS '本地或对象存储类型';
COMMENT ON COLUMN files.storage_key IS '文件在存储中的对象键';
COMMENT ON COLUMN files.original_name IS '用户上传的原始文件名';
COMMENT ON COLUMN files.content_type IS 'MIME 类型';
COMMENT ON COLUMN files.byte_size IS '文件字节数';
COMMENT ON COLUMN files.status IS '文件生命周期状态';
COMMENT ON COLUMN files.etag IS '对象存储 ETag';
COMMENT ON COLUMN files.uploaded_at IS '上传完成时间';
COMMENT ON COLUMN files.expires_at IS '临时文件过期或删除任务执行时间';
COMMENT ON COLUMN files.multipart_upload_id IS '对象存储分片上传会话编号';
COMMENT ON COLUMN files.part_size IS '分片字节数，0 表示整体上传';

-- +goose Down
DROP TABLE files;
