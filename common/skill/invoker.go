package skill

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Invoker 技能执行器，负责调用技能并自动完成计时和日志记录
type Invoker struct {
	mu      sync.RWMutex
	logger  InvocationLogger
	checker UserSkillChecker
}

var globalInvoker = &Invoker{}

// GetInvoker 返回全局执行器单例
func GetInvoker() *Invoker {
	return globalInvoker
}

// SetLogger 注入调用日志实现（由 main.go 在启动时注入，避免循环依赖）
func (inv *Invoker) SetLogger(logger InvocationLogger) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.logger = logger
}

// SetChecker 注入用户技能启用状态检查函数
func (inv *Invoker) SetChecker(checker UserSkillChecker) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.checker = checker
}

// IsEnabledForUser 检查用户是否启用了某技能（未注入 checker 时默认允许）
func (inv *Invoker) IsEnabledForUser(userName, skillCode string) bool {
	inv.mu.RLock()
	checker := inv.checker
	inv.mu.RUnlock()

	if checker == nil {
		return true
	}
	return checker(userName, skillCode)
}

// Invoke 执行技能，自动记录调用耗时和日志
func (inv *Invoker) Invoke(ctx context.Context, s Skill, req *ExecuteRequest) (*ExecuteResult, error) {
	traceID := uuid.New().String()
	start := time.Now()

	inputJSON, _ := json.Marshal(req)

	result, err := s.Execute(ctx, req)

	latencyMs := time.Since(start).Milliseconds()
	status := "success"
	errMsg := ""
	outputJSON := "null"

	if err != nil {
		status = "failed"
		errMsg = err.Error()
		log.Printf("[Skill] code=%s trace=%s latency=%dms status=failed err=%v", s.Code(), traceID, latencyMs, err)
	} else {
		log.Printf("[Skill] code=%s trace=%s latency=%dms status=success", s.Code(), traceID, latencyMs)
		if result != nil {
			if b, e := json.Marshal(result); e == nil {
				outputJSON = string(b)
			}
		}
	}

	inv.mu.RLock()
	logger := inv.logger
	inv.mu.RUnlock()

	if logger != nil {
		logger.Log(
			traceID,
			req.UserName,
			req.SessionID,
			s.Code(),
			string(inputJSON),
			outputJSON,
			status,
			latencyMs,
			errMsg,
		)
	}

	return result, err
}
