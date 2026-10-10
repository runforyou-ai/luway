-- +goose Up
-- 为 AI 模型来源路由增加权重，并为按来源读取最近结束的上游尝试建立索引。
ALTER TABLE ai_model_routes ADD COLUMN weight integer NOT NULL DEFAULT 1;
COMMENT ON TABLE ai_model_routes IS 'AI 模型来源路由，调用模型时按权重随机排出已启用来源的尝试顺序，权重为 0 的来源在其后按优先级尝试；同一供应商内上游模型标识唯一，在事务提交时校验';
COMMENT ON COLUMN ai_model_routes.priority IS '来源的排列顺序，数值小的在前；权重为 0 的来源按此顺序尝试';
COMMENT ON COLUMN ai_model_routes.weight IS '被选为先尝试来源的相对权重，0 表示只作备用';
COMMENT ON COLUMN ai_model_call_attempts.status IS '尝试状态：running 进行中、succeeded 成功、failed 失败、canceled 调用方取消、时限到期或进程中断、timed_out 上游超时';
CREATE INDEX ai_model_call_attempts_route_idx ON ai_model_call_attempts (route_id, finished_at DESC, id DESC);

-- +goose Down
DROP INDEX ai_model_call_attempts_route_idx;
COMMENT ON COLUMN ai_model_call_attempts.status IS '尝试状态：running 进行中、succeeded 成功、failed 失败、canceled 已取消、timed_out 超时';
COMMENT ON COLUMN ai_model_routes.priority IS '尝试顺序，数值小的先尝试';
COMMENT ON TABLE ai_model_routes IS 'AI 模型来源路由，调用模型时按优先级依次尝试已启用的来源；同一供应商内上游模型标识唯一，在事务提交时校验';
ALTER TABLE ai_model_routes DROP COLUMN weight;
