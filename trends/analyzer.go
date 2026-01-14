package trends

import (
	"sort"
	"time"

	"atropos/history"
)

type Analyzer struct {
	history *history.HistoryManager
}

func NewAnalyzer(historyMgr *history.HistoryManager) *Analyzer {
	return &Analyzer{
		history: historyMgr,
	}
}

type NodeTrend struct {
	Node         string         `json:"node"`
	TotalCuts    int            `json:"total_cuts"`
	SuccessRate  float64        `json:"success_rate"`
	AvgLatencyMs int64          `json:"avg_latency_ms"`
	ByAction     map[string]int `json:"by_action"`
	MostCommon   string         `json:"most_common_action"`
	LastCut      *time.Time     `json:"last_cut,omitempty"`
	FirstCut     *time.Time     `json:"first_cut,omitempty"`
}

type ActionStats struct {
	Action       string     `json:"action"`
	TotalCuts    int        `json:"total_cuts"`
	Success      int        `json:"success"`
	Failed       int        `json:"failed"`
	SuccessRate  float64    `json:"success_rate"`
	AvgLatencyMs int64      `json:"avg_latency_ms"`
	UsedByNodes  []string   `json:"used_by_nodes"`
	LastExecuted *time.Time `json:"last_executed,omitempty"`
}

type GlobalTrend struct {
	PeriodDays       int             `json:"period_days"`
	TotalCuts        int             `json:"total_cuts"`
	SuccessRate      float64         `json:"success_rate"`
	ByNode           map[string]int  `json:"by_node"`
	ByAction         map[string]int  `json:"by_action"`
	NodeTrends       []*NodeTrend    `json:"node_trends"`
	ActionStats      []*ActionStats  `json:"action_stats"`
	MTTR             *time.Duration  `json:"mttr,omitempty"`
	ProblematicNodes []*NodeTrend    `json:"problematic_nodes"`
	Timeline         []TimelineEntry `json:"timeline"`
}

type TimelineEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Node      string    `json:"node"`
	Action    string    `json:"action"`
	Success   bool      `json:"success"`
	Entropy   float64   `json:"entropy"`
}

func (a *Analyzer) GetNodeTrends(node string) (*NodeTrend, error) {
	cuts, err := a.history.ListCutsByNode(node, 0)
	if err != nil {
		return nil, err
	}

	if len(cuts) == 0 {
		return &NodeTrend{
			Node:        node,
			ByAction:    make(map[string]int),
			SuccessRate: 1.0,
		}, nil
	}

	trend := &NodeTrend{
		Node:         node,
		TotalCuts:    len(cuts),
		ByAction:     make(map[string]int),
		AvgLatencyMs: 0,
	}

	var totalLatency int64
	var successCount int
	var mostCommon string
	var mostCommonCount int

	for _, cut := range cuts {
		if trend.FirstCut == nil || cut.Timestamp.Before(*trend.FirstCut) {
			trend.FirstCut = &cut.Timestamp
		}
		if trend.LastCut == nil || cut.Timestamp.After(*trend.LastCut) {
			trend.LastCut = &cut.Timestamp
		}

		trend.ByAction[cut.Action]++
		if trend.ByAction[cut.Action] > mostCommonCount {
			mostCommon = cut.Action
			mostCommonCount = trend.ByAction[cut.Action]
		}

		totalLatency += cut.LatencyMs
		if cut.Success {
			successCount++
		}
	}

	trend.SuccessRate = float64(successCount) / float64(trend.TotalCuts) * 100
	if trend.TotalCuts > 0 {
		trend.AvgLatencyMs = totalLatency / int64(trend.TotalCuts)
	}
	trend.MostCommon = mostCommon

	return trend, nil
}

func (a *Analyzer) GetActionStats() ([]*ActionStats, error) {
	allCuts, err := a.history.ListCuts(0)
	if err != nil {
		return nil, err
	}

	actions := make(map[string]*ActionStats)

	for _, cut := range allCuts {
		if actions[cut.Action] == nil {
			actions[cut.Action] = &ActionStats{
				Action:       cut.Action,
				TotalCuts:    0,
				Success:      0,
				Failed:       0,
				SuccessRate:  0,
				AvgLatencyMs: 0,
				UsedByNodes:  []string{},
			}
		}

		stats := actions[cut.Action]
		stats.TotalCuts++
		if cut.Success {
			stats.Success++
		} else {
			stats.Failed++
		}

		if stats.LastExecuted == nil || cut.Timestamp.After(*stats.LastExecuted) {
			stats.LastExecuted = &cut.Timestamp
		}

		nodeFound := false
		for _, n := range stats.UsedByNodes {
			if n == cut.Node {
				nodeFound = true
				break
			}
		}
		if !nodeFound {
			stats.UsedByNodes = append(stats.UsedByNodes, cut.Node)
		}
	}

	var result []*ActionStats
	for _, stats := range actions {
		if stats.TotalCuts > 0 {
			stats.SuccessRate = float64(stats.Success) / float64(stats.TotalCuts) * 100
		}
		result = append(result, stats)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].TotalCuts > result[j].TotalCuts
	})

	return result, nil
}

func (a *Analyzer) GetGlobalTrends(days int) (*GlobalTrend, error) {
	allCuts, err := a.history.ListCuts(0)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	var recentCuts []*history.CutRecord
	for _, cut := range allCuts {
		if cut.Timestamp.After(cutoff) {
			recentCuts = append(recentCuts, cut)
		}
	}

	trend := &GlobalTrend{
		PeriodDays: days,
		TotalCuts:  len(recentCuts),
		ByNode:     make(map[string]int),
		ByAction:   make(map[string]int),
		Timeline:   []TimelineEntry{},
	}

	var totalSuccess int
	var successRate float64

	for _, cut := range recentCuts {
		trend.ByNode[cut.Node]++
		trend.ByAction[cut.Action]++

		trend.Timeline = append(trend.Timeline, TimelineEntry{
			Timestamp: cut.Timestamp,
			Node:      cut.Node,
			Action:    cut.Action,
			Success:   cut.Success,
			Entropy:   cut.Entropy,
		})

		if cut.Success {
			totalSuccess++
		}
	}

	if trend.TotalCuts > 0 {
		successRate = float64(totalSuccess) / float64(trend.TotalCuts) * 100
	}
	trend.SuccessRate = successRate

	mttr := a.calculateMTTR(recentCuts)
	if mttr != nil {
		trend.MTTR = mttr
	}

	problematicNodes := a.identifyProblematicNodes(recentCuts)
	trend.ProblematicNodes = problematicNodes

	actionStats, err := a.GetActionStats()
	if err != nil {
		return nil, err
	}

	var filteredActionStats []*ActionStats
	for _, stats := range actionStats {
		for _, cut := range recentCuts {
			if stats.Action == cut.Action {
				filteredActionStats = append(filteredActionStats, stats)
				break
			}
		}
	}
	trend.ActionStats = filteredActionStats

	nodes := make(map[string]bool)
	for _, cut := range recentCuts {
		nodes[cut.Node] = true
	}

	for node := range nodes {
		nodeTrend, err := a.GetNodeTrends(node)
		if err != nil {
			continue
		}
		trend.NodeTrends = append(trend.NodeTrends, nodeTrend)
	}

	sort.Slice(trend.Timeline, func(i, j int) bool {
		return trend.Timeline[i].Timestamp.Before(trend.Timeline[j].Timestamp)
	})

	return trend, nil
}

func (a *Analyzer) calculateMTTR(cuts []*history.CutRecord) *time.Duration {
	var successfulCuts []*history.CutRecord
	for _, cut := range cuts {
		if cut.Success {
			successfulCuts = append(successfulCuts, cut)
		}
	}

	if len(successfulCuts) < 2 {
		return nil
	}

	sorted := make([]*history.CutRecord, len(successfulCuts))
	copy(sorted, successfulCuts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	var totalInterval time.Duration
	var count int
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Node == sorted[i-1].Node {
			interval := sorted[i].Timestamp.Sub(sorted[i-1].Timestamp)
			totalInterval += interval
			count++
		}
	}

	if count == 0 {
		return nil
	}

	avg := totalInterval / time.Duration(count)
	return &avg
}

func (a *Analyzer) identifyProblematicNodes(cuts []*history.CutRecord) []*NodeTrend {
	nodeCutCount := make(map[string]int)
	nodeFailCount := make(map[string]int)

	for _, cut := range cuts {
		nodeCutCount[cut.Node]++
		if !cut.Success {
			nodeFailCount[cut.Node]++
		}
	}

	var problematic []*NodeTrend
	for node := range nodeCutCount {
		totalCuts := nodeCutCount[node]
		failedCuts := nodeFailCount[node]

		if totalCuts >= 3 && failedCuts > 0 {
			nodeTrend, err := a.GetNodeTrends(node)
			if err != nil {
				continue
			}
			problematic = append(problematic, nodeTrend)
		}
	}

	sort.Slice(problematic, func(i, j int) bool {
		return problematic[i].TotalCuts > problematic[j].TotalCuts
	})

	if len(problematic) > 5 {
		problematic = problematic[:5]
	}

	return problematic
}
