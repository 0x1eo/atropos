package correlation

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type ClothoFinding struct {
	ControlID     string                 `json:"control_id"`
	ControlTitle  string                 `json:"control_title"`
	CollectorType string                 `json:"collector_type"`
	Node          string                 `json:"node"`
	Passed        bool                   `json:"passed"`
	Evidence      map[string]interface{} `json:"evidence"`
	Command       string                 `json:"command"`
	Timestamp     string                 `json:"timestamp"`
}

type ClothoReport struct {
	AuditID         string          `json:"audit_id"`
	BaselineVersion string          `json:"baseline_version"`
	Standard        string          `json:"standard"`
	Organization    string          `json:"organization"`
	GeneratedAt     string          `json:"generated_at"`
	Nodes           []string        `json:"nodes"`
	Findings        []ClothoFinding `json:"findings"`
	Summary         struct {
		TotalChecks     int                    `json:"total_checks"`
		Passed          int                    `json:"passed"`
		Failed          int                    `json:"failed"`
		PassRate        float64                `json:"pass_rate"`
		ByControl       map[string]interface{} `json:"by_control"`
		EntropyDetected bool                   `json:"entropy_detected"`
	} `json:"summary"`
}

type CorrelationResult struct {
	Findings      []ClothoFinding `json:"findings"`
	Cuts          []CutReference  `json:"cuts"`
	Remediated    []Correlation   `json:"remediated"`
	Unresolved    []ClothoFinding `json:"unresolved"`
	Effectiveness float64         `json:"effectiveness"`
}

type CutReference struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	Success   bool      `json:"success"`
}

type Correlation struct {
	Finding   ClothoFinding `json:"finding"`
	Cut       *CutReference `json:"cut"`
	TimeDelta time.Duration `json:"time_delta"`
	Resolved  bool          `json:"resolved"`
}

type ClothoImporter struct {
	reports map[string]ClothoReport
}

func NewClothoImporter() *ClothoImporter {
	return &ClothoImporter{
		reports: make(map[string]ClothoReport),
	}
}

func (ci *ClothoImporter) ImportReport(r io.Reader) (*ClothoReport, error) {
	var report ClothoReport
	if err := json.NewDecoder(r).Decode(&report); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}

	ci.reports[report.AuditID] = report
	return &report, nil
}

func (ci *ClothoImporter) GetReport(auditID string) (*ClothoReport, bool) {
	report, ok := ci.reports[auditID]
	return &report, ok
}

func (ci *ClothoImporter) ListReports() []ClothoReport {
	var reports []ClothoReport
	for _, report := range ci.reports {
		reports = append(reports, report)
	}
	return reports
}

type Correlator struct {
	importer *ClothoImporter
	cutRefs  []CutReference
}

func NewCorrelator(importer *ClothoImporter, cutRefs []CutReference) *Correlator {
	return &Correlator{
		importer: importer,
		cutRefs:  cutRefs,
	}
}

func (c *Correlator) Correlate(node string, timeWindow time.Duration) (*CorrelationResult, error) {
	var failedFindings []ClothoFinding
	var cutsInWindow []CutReference

	for _, report := range c.importer.ListReports() {
		for _, finding := range report.Findings {
			if finding.Node != node {
				continue
			}

			if !finding.Passed {
				failedFindings = append(failedFindings, finding)
			}
		}
	}

	for _, cut := range c.cutRefs {
		if cut.Timestamp.After(time.Now().Add(-timeWindow)) {
			cutsInWindow = append(cutsInWindow, cut)
		}
	}

	var correlations []Correlation
	var resolved []Correlation

	for _, finding := range failedFindings {
		findingTime, err := time.Parse(time.RFC3339, finding.Timestamp)
		if err != nil {
			continue
		}

		var matchedCut *CutReference
		for j := range cutsInWindow {
			cut := &cutsInWindow[j]
			timeDelta := cut.Timestamp.Sub(findingTime)

			if timeDelta >= 0 && timeDelta <= timeWindow {
				matchedCut = cut
				correlation := Correlation{
					Finding:   finding,
					Cut:       matchedCut,
					TimeDelta: timeDelta,
					Resolved:  matchedCut.Success,
				}
				correlations = append(correlations, correlation)
				resolved = append(resolved, correlation)
				break
			}
		}
	}

	var unresolved []ClothoFinding
	for _, finding := range failedFindings {
		isResolved := false
		for _, corr := range resolved {
			if corr.Finding.ControlID == finding.ControlID &&
				corr.Finding.CollectorType == finding.CollectorType &&
				corr.Finding.Timestamp == finding.Timestamp {
				isResolved = true
				break
			}
		}
		if !isResolved {
			unresolved = append(unresolved, finding)
		}
	}

	effectiveness := 0.0
	if len(failedFindings) > 0 {
		effectiveness = float64(len(resolved)) / float64(len(failedFindings)) * 100
	}

	return &CorrelationResult{
		Findings:      failedFindings,
		Cuts:          cutsInWindow,
		Remediated:    resolved,
		Unresolved:    unresolved,
		Effectiveness: effectiveness,
	}, nil
}

func (c *Correlator) GetTriggeringControls(node string) (map[string]int, error) {
	var findings []ClothoFinding

	for _, report := range c.importer.ListReports() {
		for _, finding := range report.Findings {
			if finding.Node == node && !finding.Passed {
				findings = append(findings, finding)
			}
		}
	}

	triggerCounts := make(map[string]int)
	for _, finding := range findings {
		triggerCounts[finding.ControlID]++
	}

	return triggerCounts, nil
}
