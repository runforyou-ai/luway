package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// planToolNames 是任务清单工具，按注册顺序排列。
var planToolNames = []string{plantask.TaskCreateToolName, plantask.TaskGetToolName, plantask.TaskUpdateToolName, plantask.TaskListToolName}

// planTaskDir 是任务文件在运行内存中的目录。
const planTaskDir = "/plan"

// planStore 在运行内存中保存任务清单工具读写的任务文件，每次写入或删除任务后把展示用的清单交给过程记录器。
// 全部任务完成后框架会删除任务文件，展示用的清单保留这些任务的完成状态。
type planStore struct {
	recorder *processRecorder
	mu       sync.Mutex
	files    map[string]string
	shown    []runstream.PlanTask // 按任务编号排列的展示清单。
}

// newPlanMiddleware 创建任务清单中间件并返回任务存储，任务保存在本次运行的内存与恢复状态中。
func newPlanMiddleware(ctx context.Context, recorder *processRecorder) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], *planStore, error) {
	store := &planStore{recorder: recorder, files: make(map[string]string)}
	middleware, err := plantask.NewTyped[*schema.AgenticMessage](ctx, &plantask.Config{Backend: store, BaseDir: planTaskDir})
	if err != nil {
		return nil, nil, fmt.Errorf("create plan task middleware: %w", err)
	}
	return middleware, store, nil
}

// snapshot 返回任务文件的副本。
func (s *planStore) snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.files)
}

// restore 写回已保存的任务文件与展示清单。
func (s *planStore) restore(files map[string]string, shown []runstream.PlanTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = maps.Clone(files)
	if s.files == nil {
		s.files = make(map[string]string)
	}
	s.shown = slices.Clone(shown)
}

// LsInfo 列出目录下的文件。
func (s *planStore) LsInfo(_ context.Context, req *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Clean(req.Path)
	var infos []plantask.FileInfo
	for path, content := range s.files {
		if filepath.Dir(path) == dir {
			infos = append(infos, plantask.FileInfo{Path: path, Size: int64(len(content))})
		}
	}
	slices.SortFunc(infos, func(a, b plantask.FileInfo) int { return strings.Compare(a.Path, b.Path) })
	return infos, nil
}

// Read 读取文件的完整内容。
func (s *planStore) Read(_ context.Context, req *plantask.ReadRequest) (*filesystem.FileContent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.files[filepath.Clean(req.FilePath)]
	if !ok {
		return nil, fmt.Errorf("%s: %w", req.FilePath, os.ErrNotExist)
	}
	return &filesystem.FileContent{Content: content}, nil
}

// Write 写入文件，写入的是任务文件时更新展示清单。
func (s *planStore) Write(_ context.Context, req *plantask.WriteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Clean(req.FilePath)
	s.files[path] = req.Content
	task, ok := planTaskFile(path, req.Content)
	if !ok {
		return nil
	}
	if index := slices.IndexFunc(s.shown, func(item runstream.PlanTask) bool { return item.ID == task.ID }); index >= 0 {
		s.shown[index] = task
	} else {
		s.shown = append(s.shown, task)
		// 按任务编号的数值排列。
		slices.SortFunc(s.shown, func(a, b runstream.PlanTask) int {
			x, _ := strconv.Atoi(a.ID)
			y, _ := strconv.Atoi(b.ID)
			return x - y
		})
	}
	s.recorder.setPlan(slices.Clone(s.shown))
	return nil
}

// Delete 删除文件；删除任务时从展示清单移除，剩余任务文件全部已完成时视为框架清理，展示清单保持不变。
func (s *planStore) Delete(_ context.Context, req *plantask.DeleteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Clean(req.FilePath)
	content, exists := s.files[path]
	if !exists {
		return nil
	}
	delete(s.files, path)
	task, ok := planTaskFile(path, content)
	if !ok {
		return nil
	}
	cleanup := task.Status == domain.AgentPlanTaskCompleted
	for other, otherContent := range s.files {
		if remaining, ok := planTaskFile(other, otherContent); ok && remaining.Status != domain.AgentPlanTaskCompleted {
			cleanup = false
		}
	}
	if cleanup {
		return nil
	}
	s.shown = slices.DeleteFunc(s.shown, func(item runstream.PlanTask) bool { return item.ID == task.ID })
	s.recorder.setPlan(slices.Clone(s.shown))
	return nil
}

// planTaskFile 解析任务文件，路径不是任务文件或内容无法解析时返回 false。
func planTaskFile(path, content string) (runstream.PlanTask, bool) {
	id, found := strings.CutSuffix(filepath.Base(path), ".json")
	if !found {
		return runstream.PlanTask{}, false
	}
	if _, err := strconv.Atoi(id); err != nil {
		return runstream.PlanTask{}, false
	}
	var task runstream.PlanTask
	if err := json.Unmarshal([]byte(content), &task); err != nil {
		return runstream.PlanTask{}, false
	}
	return task, true
}
