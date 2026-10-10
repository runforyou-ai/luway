-- +goose Up
-- 创建客服周期质检表，每个客服处理周期最多一条，对应周期最近一次关闭。
CREATE TABLE service_session_reviews (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    workspace_id         uuid NOT NULL,
    service_session_id   uuid NOT NULL,
    closed_at            timestamptz NOT NULL,
    satisfaction         text,
    ai_incorrect         boolean,
    ai_missed_handoff    boolean,
    ai_poor_attitude     boolean,
    human_incorrect      boolean,
    human_poor_attitude  boolean
);

CREATE UNIQUE INDEX service_session_reviews_service_session_unique
    ON service_session_reviews (workspace_id, service_session_id);

CREATE TRIGGER service_session_reviews_set_updated_at BEFORE UPDATE ON service_session_reviews FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE service_session_reviews IS '客服周期质检：判断模型在周期关闭后给出的满意度与 AI 客服、真人客服质检结论';
COMMENT ON COLUMN service_session_reviews.id IS '质检编号';
COMMENT ON COLUMN service_session_reviews.created_at IS '创建时间';
COMMENT ON COLUMN service_session_reviews.updated_at IS '更新时间';
COMMENT ON COLUMN service_session_reviews.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN service_session_reviews.service_session_id IS '客服处理周期编号';
COMMENT ON COLUMN service_session_reviews.closed_at IS '质检对应的周期关闭时间';
COMMENT ON COLUMN service_session_reviews.satisfaction IS '推断满意度：satisfied 满意、neutral 一般、dissatisfied 不满意，未判定时为空';
COMMENT ON COLUMN service_session_reviews.ai_incorrect IS 'AI 客服答复有误；周期内没有 AI 对客回复时为空';
COMMENT ON COLUMN service_session_reviews.ai_missed_handoff IS 'AI 客服应转人工而未转；周期不是 AI 独立处理时为空';
COMMENT ON COLUMN service_session_reviews.ai_poor_attitude IS 'AI 客服态度问题；周期内没有 AI 对客回复时为空';
COMMENT ON COLUMN service_session_reviews.human_incorrect IS '真人客服答复有误；周期内没有真人对客回复时为空';
COMMENT ON COLUMN service_session_reviews.human_poor_attitude IS '真人客服态度问题；周期内没有真人对客回复时为空';

-- +goose Down
DROP TABLE service_session_reviews;
