package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"atropos/cutter"
	"atropos/history"
	"atropos/internal/logger"
	"atropos/notifications"
	"atropos/policy"
)

type Executor struct {
	policy        *policy.RemediationPolicy
	registry      *cutter.Registry
	history       *history.HistoryManager
	rateLimiter   *RateLimiter
	notifications *notifications.NotificationManager
	mu            sync.Mutex
}

type RateLimiter struct {
	nodeCounts map[string]rateLimitEntry
	mu         sync.Mutex
}

type rateLimitEntry struct {
	count       int
	windowStart time.Time
	limit       *policy.RateLimit
}

func NewExecutor(pol *policy.RemediationPolicy, history *history.HistoryManager, notif *notifications.NotificationManager) *Executor {
	return &Executor{
		policy:        pol,
		registry:      cutter.NewRegistry(),
		history:       history,
		notifications: notif,
		rateLimiter: &RateLimiter{
			nodeCounts: make(map[string]rateLimitEntry),
		},
	}
}

func (rl *RateLimiter) checkRateLimit(node string, rateLimit *policy.RateLimit) (bool, time.Duration, error) {
	if rateLimit == nil || rateLimit.MaxCuts == 0 {
		return true, 0, nil
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.nodeCounts[node]
	windowDuration := time.Duration(rateLimit.Window) * time.Minute

	if !exists || now.Sub(entry.windowStart) > windowDuration {
		rl.nodeCounts[node] = rateLimitEntry{
			count:       1,
			windowStart: now,
			limit:       rateLimit,
		}
		return true, windowDuration, nil
	}

	if entry.count >= rateLimit.MaxCuts {
		timeUntilReset := entry.windowStart.Add(windowDuration).Sub(now)
		return false, timeUntilReset, fmt.Errorf("rate limit exceeded: %d cuts per %d minutes", rateLimit.MaxCuts, rateLimit.Window)
	}

	entry.count++
	rl.nodeCounts[node] = entry
	return true, windowDuration, nil
}

func (e *Executor) GetHistory() *history.HistoryManager {
	return e.history
}

func (e *Executor) GetPolicy() *policy.RemediationPolicy {
	return e.policy
}

func (e *Executor) checkTimeWindows(nodePolicy *policy.NodePolicy) error {
	if len(nodePolicy.TimeWindows) == 0 {
		return nil
	}

	now := time.Now()
	currentTime := now.Format("15:04")

	for _, window := range nodePolicy.TimeWindows {
		if currentTime >= window.Start && currentTime <= window.End {
			return nil
		}
	}

	return fmt.Errorf("outside allowed time windows for node %s", nodePolicy.Name)
}

func (e *Executor) ExecuteCut(ctx context.Context, node string, entropy float64) *cutter.CutResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	nodePolicy, ok := e.policy.GetNode(node)
	if !ok {
		result := &cutter.CutResult{
			Target:  node,
			Success: false,
			Error:   fmt.Errorf("unknown node: %s", node),
		}
		e.logCut(node, entropy, &policy.Strategy{}, result, 0)
		return result
	}

	if err := e.checkTimeWindows(nodePolicy); err != nil {
		result := &cutter.CutResult{
			Target:  node,
			Success: false,
			Error:   err,
		}
		e.logCut(node, entropy, &policy.Strategy{}, result, 0)
		return result
	}

	strategy, ok := nodePolicy.SelectStrategy(entropy)
	if !ok {
		result := &cutter.CutResult{
			Target:  node,
			Action:  "none",
			Success: true,
		}
		e.logCut(node, entropy, &policy.Strategy{Action: "none", Threshold: 0}, result, 0)
		return result
	}

	if allowed, _, err := e.rateLimiter.checkRateLimit(node, nodePolicy.RateLimit); !allowed {
		result := &cutter.CutResult{
			Target:  node,
			Success: false,
			Error:   err,
		}
		e.logCut(node, entropy, strategy, result, 0)
		return result
	}

	logger.CutInitiated(node, strategy.Action, entropy)

	result := e.executeStrategy(ctx, node, nodePolicy, strategy)

	if !result.Success {
		if strategy.OnFailure != "" {
			fallbackStrategy, ok := nodePolicy.SelectStrategyByAction(strategy.OnFailure)
			if ok {
				logger.Get().Warn("fallback_strategy",
					zap.String("node", node),
					zap.String("original_action", strategy.Action),
					zap.String("fallback_action", fallbackStrategy.Action),
				)
				return e.executeStrategy(ctx, node, nodePolicy, fallbackStrategy)
			}
		}

		if strategy.Critical {
			if escalated, ok := nodePolicy.GetEscalationStrategy(strategy.Threshold); ok {
				logger.Escalation(node, strategy.Action, escalated.Action, result.Error.Error())
				return e.executeStrategy(ctx, node, nodePolicy, escalated)
			}
		}
	}

	return result
}

func (e *Executor) executeStrategy(ctx context.Context, node string, nodePolicy *policy.NodePolicy, strategy *policy.Strategy) *cutter.CutResult {
	start := time.Now()

	c, ok := e.registry.FindCutter(strategy.Action)
	if !ok {
		err := fmt.Errorf("no cutter for action: %s", strategy.Action)
		logger.CutFailed(node, strategy.Action, err)
		result := &cutter.CutResult{
			Target:  node,
			Action:  strategy.Action,
			Success: false,
			Error:   err,
		}
		e.logCut(node, 0, strategy, result, 0)
		return result
	}

	params := map[string]string{
		"action":        strategy.Action,
		"command":       strategy.Command,
		"snapshot_name": strategy.SnapshotName,
		"host":          nodePolicy.Host,
		"user":          nodePolicy.User,
	}
	if nodePolicy.Port > 0 {
		params["port"] = fmt.Sprintf("%d", nodePolicy.Port)
	}

	cutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.Execute(cutCtx, node, params)
	latency := time.Since(start).Milliseconds()

	var result *cutter.CutResult
	if err != nil {
		logger.CutFailed(node, strategy.Action, err)
		result = &cutter.CutResult{
			Target:    node,
			Action:    strategy.Action,
			Success:   false,
			Error:     err,
			LatencyMs: latency,
		}
	} else {
		logger.CutExecuted(node, strategy.Action, latency)
		result = &cutter.CutResult{
			Target:    node,
			Action:    strategy.Action,
			Success:   true,
			LatencyMs: latency,
		}
	}

	e.logCut(node, strategy.Threshold, strategy, result, latency)
	return result
}

func (e *Executor) logCut(node string, entropy float64, strategy *policy.Strategy, result *cutter.CutResult, latency int64) {
	if e.history == nil {
		return
	}

	policyVer := ""
	if e.policy != nil && e.policy.Meta.Version != "" {
		policyVer = e.policy.Meta.Version
	}

	timestamp := time.Now().UTC()
	record := &history.CutRecord{
		ID:            fmt.Sprintf("cut_%d_%s", timestamp.Unix(), node),
		Node:          node,
		Entropy:       entropy,
		Timestamp:     timestamp,
		PolicyVersion: policyVer,
		Strategy: history.StrategyInfo{
			Threshold:    strategy.Threshold,
			Action:       strategy.Action,
			Critical:     strategy.Critical,
			SnapshotName: strategy.SnapshotName,
			Command:      strategy.Command,
		},
	}

	if result != nil {
		record.Action = result.Action
		record.Success = result.Success
		record.LatencyMs = result.LatencyMs
		if result.Error != nil {
			record.Error = result.Error.Error()
		}
	}

	if err := e.history.SaveCut(record); err != nil {
		logger.Get().Error("failed_to_save_cut_history",
			zap.Error(err),
			zap.String("node", node),
			zap.String("action", record.Action),
		)
	}

	if e.notifications != nil {
		event := &notifications.CutEvent{
			ID:        record.ID,
			Node:      node,
			Action:    record.Action,
			Success:   record.Success,
			Entropy:   entropy,
			LatencyMs: record.LatencyMs,
			Timestamp: record.Timestamp,
		}
		if result != nil && result.Error != nil {
			event.Error = result.Error.Error()
		}

		if err := e.notifications.NotifyCut(event); err != nil {
			logger.Get().Error("failed_to_send_notification",
				zap.Error(err),
				zap.String("node", node),
				zap.String("action", record.Action),
			)
		}
	}
}

func (e *Executor) ExecuteCutAsync(ctx context.Context, node string, entropy float64) <-chan *cutter.CutResult {
	ch := make(chan *cutter.CutResult, 1)
	go func() {
		defer close(ch)
		ch <- e.ExecuteCut(ctx, node, entropy)
	}()
	return ch
}
