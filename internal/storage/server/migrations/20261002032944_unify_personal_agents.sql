-- +goose Up
-- 删除 AI 员工主人字段，记忆表改名为 AI 员工记忆表。
ALTER TABLE agents DROP COLUMN owner_user_id;

COMMENT ON TABLE agents IS 'AI 员工';
COMMENT ON COLUMN agents.id IS 'AI 员工编号';
COMMENT ON COLUMN agents.device_id IS '仅服务负责人本人的 AI 员工绑定的电脑编号，其他 AI 员工为空';
COMMENT ON COLUMN agents.paused_at IS '负责人暂停仅服务本人的 AI 员工的时间，非空表示暂停';
COMMENT ON COLUMN agents.service_audiences IS 'AI 员工的服务对象：customer 客户、employee 本工作区成员，或单独的 personal 仅负责人本人';
COMMENT ON COLUMN agents.responsible_user_id IS 'AI 员工负责人编号；仅服务负责人本人时必填，其他 AI 员工为空表示未指定';

ALTER TABLE assistant_memories RENAME TO agent_memories;
ALTER INDEX assistant_memories_agent_id_path_key RENAME TO agent_memories_agent_id_path_key;

COMMENT ON TABLE agent_memories IS '仅服务负责人本人的 AI 员工的长期记忆条目';
COMMENT ON COLUMN agent_memories.agent_id IS '所属 AI 员工编号';
COMMENT ON COLUMN agent_memories.path IS '记忆目录下的文件名，同一 AI 员工内唯一';

COMMENT ON COLUMN agent_runs.execution_device_id IS '执行设备编号，仅服务负责人本人的 AI 员工的运行为其绑定电脑，其他运行为空';
COMMENT ON COLUMN agent_conversations.memory_extracted_seq IS 'AI 员工记忆已提取到的会话消息序号';

-- +goose Down
COMMENT ON COLUMN agent_conversations.memory_extracted_seq IS '助理记忆已提取到的会话消息序号';
COMMENT ON COLUMN agent_runs.execution_device_id IS '执行设备编号，助理的运行为其绑定电脑，AI 员工的运行为空';

ALTER INDEX agent_memories_agent_id_path_key RENAME TO assistant_memories_agent_id_path_key;
ALTER TABLE agent_memories RENAME TO assistant_memories;

COMMENT ON TABLE assistant_memories IS '助理的长期记忆条目';
COMMENT ON COLUMN assistant_memories.agent_id IS '所属助理编号';
COMMENT ON COLUMN assistant_memories.path IS '记忆目录下的文件名，同一助理内唯一';

ALTER TABLE agents ADD COLUMN owner_user_id uuid;

COMMENT ON TABLE agents IS 'AI 员工与助理，类型以工作区身份为准';
COMMENT ON COLUMN agents.id IS 'AI 员工或助理编号';
COMMENT ON COLUMN agents.owner_user_id IS '助理主人编号，AI 员工为空';
COMMENT ON COLUMN agents.device_id IS '助理绑定的电脑编号，AI 员工为空';
COMMENT ON COLUMN agents.paused_at IS '主人暂停助理的时间，非空表示暂停';
COMMENT ON COLUMN agents.service_audiences IS 'AI 员工的服务对象：customer 客户、employee 本工作区成员；助理为空';
COMMENT ON COLUMN agents.responsible_user_id IS 'AI 员工负责人编号，为空表示未指定，助理为空';
