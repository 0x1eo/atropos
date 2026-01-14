package history

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type CutRecord struct {
	ID            string       `json:"id"`
	Node          string       `json:"node"`
	Entropy       float64      `json:"entropy"`
	Action        string       `json:"action"`
	Success       bool         `json:"success"`
	Error         string       `json:"error,omitempty"`
	LatencyMs     int64        `json:"latency_ms"`
	Timestamp     time.Time    `json:"timestamp"`
	PolicyVersion string       `json:"policy_version"`
	Strategy      StrategyInfo `json:"strategy"`
}

type StrategyInfo struct {
	Threshold    float64 `json:"threshold"`
	Action       string  `json:"action"`
	Critical     bool    `json:"critical"`
	SnapshotName string  `json:"snapshot_name,omitempty"`
	Command      string  `json:"command,omitempty"`
}

type HistoryManager struct {
	historyDir string
	mu         sync.RWMutex
}

func NewHistoryManager(historyDir string) *HistoryManager {
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		panic(fmt.Sprintf("failed to create history directory: %v", err))
	}
	return &HistoryManager{
		historyDir: historyDir,
	}
}

func (h *HistoryManager) SaveCut(record *CutRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if record.ID == "" {
		record.ID = fmt.Sprintf("cut_%d_%s", time.Now().Unix(), record.Node)
	}

	filename := fmt.Sprintf("%s.json.gz", record.ID)
	filepath := h.joinPath(filename)

	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()

	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("encode record: %w", err)
	}

	return nil
}

func (h *HistoryManager) LoadCut(id string) (*CutRecord, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	id = strings.TrimSuffix(id, ".json.gz")
	filename := fmt.Sprintf("%s.json.gz", id)
	filepath := h.joinPath(filename)

	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	var record CutRecord
	if err := json.NewDecoder(gz).Decode(&record); err != nil {
		return nil, fmt.Errorf("decode record: %w", err)
	}

	return &record, nil
}

func (h *HistoryManager) ListCuts(limit int) ([]*CutRecord, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	entries, err := os.ReadDir(h.historyDir)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	var records []*CutRecord
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json.gz") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json.gz")
		record, err := h.LoadCut(id)
		if err != nil {
			continue
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})

	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

func (h *HistoryManager) ListCutsByNode(node string, limit int) ([]*CutRecord, error) {
	allCuts, err := h.ListCuts(0)
	if err != nil {
		return nil, err
	}

	var nodeCuts []*CutRecord
	for _, cut := range allCuts {
		if cut.Node == node {
			nodeCuts = append(nodeCuts, cut)
		}
	}

	if limit > 0 && len(nodeCuts) > limit {
		nodeCuts = nodeCuts[:limit]
	}

	return nodeCuts, nil
}

func (h *HistoryManager) GetLatestCutByNode(node string) (*CutRecord, error) {
	cuts, err := h.ListCutsByNode(node, 1)
	if err != nil {
		return nil, err
	}

	if len(cuts) == 0 {
		return nil, nil
	}

	return cuts[0], nil
}

func (h *HistoryManager) PurgeOldCuts(retentionDays int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(h.historyDir)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}

	var purged int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json.gz") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			filepath := h.joinPath(entry.Name())
			if err := os.Remove(filepath); err != nil {
				continue
			}
			purged++
		}
	}

	return nil
}

func (h *HistoryManager) GetStats() (*HistoryStats, error) {
	allCuts, err := h.ListCuts(0)
	if err != nil {
		return nil, err
	}

	stats := &HistoryStats{
		TotalCuts:   len(allCuts),
		SuccessCuts: 0,
		FailedCuts:  0,
		ByNode:      make(map[string]int),
		ByAction:    make(map[string]int),
		Nodes:       make(map[string]*NodeStats),
	}

	for _, cut := range allCuts {
		if cut.Success {
			stats.SuccessCuts++
		} else {
			stats.FailedCuts++
		}

		stats.ByNode[cut.Node]++
		stats.ByAction[cut.Action]++

		if stats.Nodes[cut.Node] == nil {
			stats.Nodes[cut.Node] = &NodeStats{
				Node:      cut.Node,
				TotalCuts: 0,
				Success:   0,
				Failed:    0,
			}
		}

		stats.Nodes[cut.Node].TotalCuts++
		if cut.Success {
			stats.Nodes[cut.Node].Success++
		} else {
			stats.Nodes[cut.Node].Failed++
		}

		if stats.FirstCut == nil || cut.Timestamp.Before(*stats.FirstCut) {
			stats.FirstCut = &cut.Timestamp
		}
		if stats.LastCut == nil || cut.Timestamp.After(*stats.LastCut) {
			stats.LastCut = &cut.Timestamp
		}
	}

	if stats.FirstCut != nil && stats.LastCut != nil {
		stats.TotalDuration = stats.LastCut.Sub(*stats.FirstCut)
	}

	return stats, nil
}

type HistoryStats struct {
	TotalCuts     int                   `json:"total_cuts"`
	SuccessCuts   int                   `json:"success_cuts"`
	FailedCuts    int                   `json:"failed_cuts"`
	FirstCut      *time.Time            `json:"first_cut,omitempty"`
	LastCut       *time.Time            `json:"last_cut,omitempty"`
	TotalDuration time.Duration         `json:"total_duration"`
	ByNode        map[string]int        `json:"by_node"`
	ByAction      map[string]int        `json:"by_action"`
	Nodes         map[string]*NodeStats `json:"nodes"`
}

type NodeStats struct {
	Node      string `json:"node"`
	TotalCuts int    `json:"total_cuts"`
	Success   int    `json:"success"`
	Failed    int    `json:"failed"`
}

func (h *HistoryManager) joinPath(filename string) string {
	return filepath.Join(h.historyDir, filename)
}
