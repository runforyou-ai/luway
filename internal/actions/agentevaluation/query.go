//go:build server

package agentevaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RunSummary 定义一次评测运行的进度与首次尝试计数，Finished 是已结束的首次尝试数。
type RunSummary struct {
	ID          string
	Status      domain.AgentEvaluationRunStatus
	CaseCount   int
	Finished    int
	Passed      int
	Failed      int
	Errors      int
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// CaseRow 定义评测页的一条用例：LatestStatus 是它在最近一次运行中首次尝试的结果，未参与时为空；RerunStatus 是该运行中最近一次单条重跑的结果，没有重跑时为空；Modified 表示用例在最近一次运行后已修改，最近结果不代表当前内容；NewFailure 表示同一用例版本由上次通过变为本次未通过。
type CaseRow struct {
	Case
	LatestStatus *domain.AgentEvaluationResultStatus
	RerunStatus  *domain.AgentEvaluationResultStatus
	Modified     bool
	NewFailure   bool
}

// Overview 定义 AI 员工评测页的数据：最近两次运行、配置是否已在最近一次运行后变更、判断模型是否可用与全部用例。
type Overview struct {
	Latest               *RunSummary
	Previous             *RunSummary
	ConfigurationChanged bool
	DecisionModelReady   bool
	Cases                []CaseRow
}

// OverviewQuery 读取 AI 员工的评测页数据。
type OverviewQuery struct{ db *bun.DB }

// NewOverviewQuery 创建评测页查询。
func NewOverviewQuery(db *bun.DB) *OverviewQuery { return &OverviewQuery{db: db} }

// Execute 按创建时间返回全部用例及其在最近一次运行中的结果。
func (q *OverviewQuery) Execute(ctx context.Context, identity *servermodels.Identity, agentID string) (*Overview, error) {
	agent, err := loadEvaluatedAgent(ctx, q.db, identity.Organization.ID, agentID)
	if err != nil {
		return nil, err
	}
	runs := make([]servermodels.AgentEvaluationRun, 0, 2)
	if err := q.db.NewSelect().Model(&runs).
		Where("aer.organization_id = ? AND aer.agent_id = ?", agent.OrganizationID, agent.ID).
		OrderExpr("aer.created_at DESC, aer.id DESC").Limit(2).Scan(ctx); err != nil {
		return nil, fmt.Errorf("load evaluation runs: %w", err)
	}
	model, err := loadDecisionModel(ctx, q.db, agent.OrganizationID)
	if err != nil {
		return nil, err
	}
	overview := &Overview{DecisionModelReady: model != nil, Cases: []CaseRow{}}
	firstAttempts := make([]map[string]servermodels.AgentEvaluationResult, len(runs))
	for index := range runs {
		results := make([]servermodels.AgentEvaluationResult, 0)
		if err := q.db.NewSelect().Model(&results).
			Column("aers.case_id", "aers.case_version", "aers.status").
			Where("aers.organization_id = ? AND aers.run_id = ? AND aers.attempt = 1", agent.OrganizationID, runs[index].ID).
			Scan(ctx); err != nil {
			return nil, fmt.Errorf("load evaluation results: %w", err)
		}
		firstAttempts[index] = make(map[string]servermodels.AgentEvaluationResult, len(results))
		finished := 0
		for _, result := range results {
			firstAttempts[index][result.CaseID] = result
			if domain.AgentEvaluationResultStatus(result.Status) != domain.AgentEvaluationResultStatusPending {
				finished++
			}
		}
		summary := runSummary(runs[index], finished)
		if index == 0 {
			overview.Latest = summary
			overview.ConfigurationChanged = runs[index].AgentRevisionID != agent.ActiveRevisionID
		} else {
			overview.Previous = summary
		}
	}
	// 读取最近一次运行中各用例最近一次单条重跑的结果。
	reruns := make(map[string]domain.AgentEvaluationResultStatus)
	if len(runs) > 0 {
		latestReruns := make([]servermodels.AgentEvaluationResult, 0)
		if err := q.db.NewSelect().Model(&latestReruns).
			DistinctOn("aers.case_id").
			Column("aers.case_id", "aers.status").
			Where("aers.organization_id = ? AND aers.run_id = ? AND aers.attempt > 1", agent.OrganizationID, runs[0].ID).
			OrderExpr("aers.case_id, aers.attempt DESC").
			Scan(ctx); err != nil {
			return nil, fmt.Errorf("load evaluation reruns: %w", err)
		}
		for _, rerun := range latestReruns {
			reruns[rerun.CaseID] = domain.AgentEvaluationResultStatus(rerun.Status)
		}
	}
	records := make([]servermodels.AgentEvaluationCase, 0)
	// 列表不读取前文快照，快照在用例详情中读取。
	if err := q.db.NewSelect().Model(&records).ExcludeColumn("context").
		Where("aec.organization_id = ? AND aec.agent_id = ?", agent.OrganizationID, agent.ID).
		OrderExpr("aec.created_at, aec.id").Scan(ctx); err != nil {
		return nil, fmt.Errorf("load evaluation cases: %w", err)
	}
	for _, record := range records {
		evaluationCase, err := caseFromRecord(&record)
		if err != nil {
			return nil, err
		}
		row := CaseRow{Case: evaluationCase}
		if len(firstAttempts) > 0 {
			if latest, ok := firstAttempts[0][record.ID]; ok {
				row.LatestStatus = new(domain.AgentEvaluationResultStatus(latest.Status))
				row.Modified = latest.CaseVersion != record.Version
				if rerun, ok := reruns[record.ID]; ok {
					row.RerunStatus = &rerun
				}
				// 同一用例版本由上次通过变为本次未通过且之后未修改时标记新失败。
				if len(firstAttempts) > 1 && !row.Modified {
					previous, ok := firstAttempts[1][record.ID]
					row.NewFailure = ok && previous.CaseVersion == latest.CaseVersion &&
						domain.AgentEvaluationResultStatus(previous.Status) == domain.AgentEvaluationResultStatusPassed &&
						domain.AgentEvaluationResultStatus(latest.Status) == domain.AgentEvaluationResultStatusFailed
				}
			}
		}
		overview.Cases = append(overview.Cases, row)
	}
	return overview, nil
}

// Attempt 定义用例在一次运行中的一次尝试，Snapshot 是运行发起时冻结的用例内容，CaseVersion 是其版本号。
type Attempt struct {
	ID                 string
	Attempt            int
	CaseVersion        int
	Snapshot           CaseSnapshot
	Status             domain.AgentEvaluationResultStatus
	ActualAction       *domain.AgentRunOutcome
	ActualReason       *domain.AgentHandoffReason
	Answer             string
	Blocks             []agentruntime.Block
	Usage              agentruntime.Usage
	CorrectProbability *float64
	ErrorCode          *domain.AgentEvaluationErrorCode
	CreatedAt          time.Time
	CompletedAt        *time.Time
}

// CaseDetail 定义用例的当前内容与它在最近一次运行中的全部尝试，按尝试序号排列。
type CaseDetail struct {
	Case     Case
	RunID    string
	Attempts []Attempt
}

// CaseDetailQuery 读取评测用例详情。
type CaseDetailQuery struct{ db *bun.DB }

// NewCaseDetailQuery 创建评测用例详情查询。
func NewCaseDetailQuery(db *bun.DB) *CaseDetailQuery { return &CaseDetailQuery{db: db} }

// Execute 返回用例与它在最近一次运行中的尝试；用例未参与最近一次运行时尝试为空。
func (q *CaseDetailQuery) Execute(ctx context.Context, identity *servermodels.Identity, agentID, caseID string) (*CaseDetail, error) {
	agent, err := loadEvaluatedAgent(ctx, q.db, identity.Organization.ID, agentID)
	if err != nil {
		return nil, err
	}
	if !common.ValidUUID(caseID) {
		return nil, ErrCaseNotFound
	}
	record := &servermodels.AgentEvaluationCase{}
	err = q.db.NewSelect().Model(record).
		Where("aec.organization_id = ? AND aec.agent_id = ? AND aec.id = ?", agent.OrganizationID, agent.ID, caseID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load evaluation case: %w", err)
	}
	evaluationCase, err := caseFromRecord(record)
	if err != nil {
		return nil, err
	}
	detail := &CaseDetail{Case: evaluationCase, Attempts: []Attempt{}}
	err = q.db.NewSelect().Model((*servermodels.AgentEvaluationRun)(nil)).Column("aer.id").
		Where("aer.organization_id = ? AND aer.agent_id = ?", agent.OrganizationID, agent.ID).
		OrderExpr("aer.created_at DESC, aer.id DESC").Limit(1).Scan(ctx, &detail.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return detail, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load latest evaluation run: %w", err)
	}
	results := make([]servermodels.AgentEvaluationResult, 0)
	if err := q.db.NewSelect().Model(&results).
		Where("aers.organization_id = ? AND aers.run_id = ? AND aers.case_id = ?", agent.OrganizationID, detail.RunID, caseID).
		OrderExpr("aers.attempt").Scan(ctx); err != nil {
		return nil, fmt.Errorf("load evaluation attempts: %w", err)
	}
	for _, result := range results {
		attempt := Attempt{
			ID: result.ID, Attempt: result.Attempt, CaseVersion: result.CaseVersion, Status: domain.AgentEvaluationResultStatus(result.Status), Answer: result.Answer,
			ActualAction: (*domain.AgentRunOutcome)(result.ActualAction), ActualReason: (*domain.AgentHandoffReason)(result.ActualReason),
			CorrectProbability: result.CorrectProbability, ErrorCode: (*domain.AgentEvaluationErrorCode)(result.Error), CreatedAt: result.CreatedAt, CompletedAt: result.CompletedAt,
		}
		if err := json.Unmarshal(result.CaseSnapshot, &attempt.Snapshot); err != nil {
			return nil, fmt.Errorf("decode evaluation case snapshot: %w", err)
		}
		if err := json.Unmarshal(result.Blocks, &attempt.Blocks); err != nil {
			return nil, fmt.Errorf("decode evaluation blocks: %w", err)
		}
		if err := json.Unmarshal(result.Usage, &attempt.Usage); err != nil {
			return nil, fmt.Errorf("decode evaluation usage: %w", err)
		}
		detail.Attempts = append(detail.Attempts, attempt)
	}
	return detail, nil
}

// loadEvaluatedAgent 读取当前企业中有服务对象的 AI 员工，个人 AI 员工与没有服务对象的 AI 员工视为不存在。
func loadEvaluatedAgent(ctx context.Context, db bun.IDB, organizationID, agentID string) (*servermodels.Agent, error) {
	return scanEvaluatedAgent(ctx, db, organizationID, agentID, false)
}

// scanEvaluatedAgent 读取当前企业中有服务对象的 AI 员工，lock 为 true 时加行锁。
func scanEvaluatedAgent(ctx context.Context, db bun.IDB, organizationID, agentID string, lock bool) (*servermodels.Agent, error) {
	if !common.ValidUUID(agentID) {
		return nil, ErrAgentNotFound
	}
	agent := &servermodels.Agent{}
	query := db.NewSelect().Model(agent).
		Where("a.organization_id = ? AND a.id = ?", organizationID, agentID).
		Where("cardinality(a.service_audiences) > 0 AND NOT ? = ANY(a.service_audiences)", domain.ServiceAudiencePersonal)
	if lock {
		query = query.For("UPDATE")
	}
	err := query.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load evaluated agent: %w", err)
	}
	return agent, nil
}

// runSummary 把运行记录转换为摘要；进行中的运行计数取自已结束的首次尝试数。
func runSummary(run servermodels.AgentEvaluationRun, finished int) *RunSummary {
	return &RunSummary{
		ID: run.ID, Status: domain.AgentEvaluationRunStatus(run.Status), CaseCount: run.CaseCount, Finished: finished,
		Passed: run.PassedCount, Failed: run.FailedCount, Errors: run.ErrorCount, CreatedAt: run.CreatedAt, CompletedAt: run.CompletedAt,
	}
}
