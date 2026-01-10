package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"atropos/cutter"
	"atropos/internal/logger"
	"atropos/policy"
)

type Executor struct {
	policy   *policy.RemediationPolicy
	registry *cutter.Registry
	mu       sync.Mutex
}

func NewExecutor(pol *policy.RemediationPolicy) *Executor {
	return &Executor{
		policy:   pol,
		registry: cutter.NewRegistry(),
	}
}

func (e *Executor) ExecuteCut(ctx context.Context, node string, entropy float64) *cutter.CutResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	nodePolicy, ok := e.policy.GetNode(node)
	if !ok {
		return &cutter.CutResult{
			Target:  node,
			Success: false,
			Error:   fmt.Errorf("unknown node: %s", node),
		}
	}

	strategy, ok := nodePolicy.SelectStrategy(entropy)
	if !ok {
		return &cutter.CutResult{
			Target:  node,
			Action:  "none",
			Success: true,
		}
	}

	logger.CutInitiated(node, strategy.Action, entropy)

	result := e.executeStrategy(ctx, node, nodePolicy, strategy)

	if !result.Success && strategy.Critical {
		if escalated, ok := nodePolicy.GetEscalationStrategy(strategy.Threshold); ok {
			logger.Escalation(node, strategy.Action, escalated.Action, result.Error.Error())
			return e.executeStrategy(ctx, node, nodePolicy, escalated)
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
		return &cutter.CutResult{
			Target:  node,
			Action:  strategy.Action,
			Success: false,
			Error:   err,
		}
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

	if err != nil {
		logger.CutFailed(node, strategy.Action, err)
		return &cutter.CutResult{
			Target:    node,
			Action:    strategy.Action,
			Success:   false,
			Error:     err,
			LatencyMs: latency,
		}
	}

	logger.CutExecuted(node, strategy.Action, latency)
	return &cutter.CutResult{
		Target:    node,
		Action:    strategy.Action,
		Success:   true,
		LatencyMs: latency,
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
