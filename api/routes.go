package api

import (
	"embed"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"atropos/correlation"
	"atropos/engine"
	"atropos/trends"
)

var DashboardFS embed.FS

type Routes struct {
	executor *engine.Executor
	analyzer *trends.Analyzer
	handler  *WebhookHandler
}

func NewRoutes(exec *engine.Executor, hmacSecret string) *Routes {
	return &Routes{
		executor: exec,
		analyzer: trends.NewAnalyzer(exec.GetHistory()),
		handler:  NewWebhookHandler(exec, hmacSecret),
	}
}

func (r *Routes) RegisterRoutes(g *gin.Engine) {
	g.GET("/", r.serveDashboard)
	g.GET("/dashboard", r.serveDashboard)
	g.Static("/static", "./dashboard/static")

	api := g.Group("/api/v1")
	{
		api.POST("/cut", r.handler.hmacMiddleware(), r.handler.handleCut)
		api.GET("/health", r.handler.handleHealth)

		history := api.Group("/cuts/history")
		{
			history.GET("", r.listCuts)
			history.GET("/:node", r.listCutsByNode)
		}

		cuts := api.Group("/cuts")
		{
			cuts.GET("/:id", r.getCut)
		}

		stats := api.Group("/stats")
		{
			stats.GET("", r.getStats)
			stats.GET("/:node", r.getNodeStats)
		}

		api.GET("/trends", r.getTrends)
		api.GET("/trends/:node", r.getNodeTrends)
		api.POST("/cut/dryrun", r.handleDryRun)

		export := api.Group("/export")
		{
			export.GET("/history.csv", r.exportCSV)
			export.GET("/history.json", r.exportJSON)
			export.GET("/report.html", r.exportHTMLReport)
		}

		api.POST("/correlation/import", r.importClothoReport)
		api.GET("/correlation/:node", r.getCorrelation)
	}
}

type StatsResponse struct {
	TotalCuts     int                        `json:"total_cuts"`
	SuccessCuts   int                        `json:"success_cuts"`
	FailedCuts    int                        `json:"failed_cuts"`
	SuccessRate   float64                    `json:"success_rate"`
	FirstCut      *string                    `json:"first_cut,omitempty"`
	LastCut       *string                    `json:"last_cut,omitempty"`
	TotalDuration int64                      `json:"total_duration_seconds"`
	ByNode        map[string]int             `json:"by_node"`
	ByAction      map[string]int             `json:"by_action"`
	Nodes         map[string]NodeStatsDetail `json:"nodes"`
}

type NodeStatsDetail struct {
	TotalCuts int `json:"total_cuts"`
	Success   int `json:"success"`
	Failed    int `json:"failed"`
}

func (r *Routes) listCuts(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "100")
	limit, _ := strconv.Atoi(limitStr)

	cuts, err := r.executor.GetHistory().ListCuts(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"count": len(cuts),
		"cuts":  cuts,
	})
}

func (r *Routes) listCutsByNode(c *gin.Context) {
	node := c.Param("node")
	limitStr := c.DefaultQuery("limit", "100")
	limit, _ := strconv.Atoi(limitStr)

	cuts, err := r.executor.GetHistory().ListCutsByNode(node, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"node":  node,
		"count": len(cuts),
		"cuts":  cuts,
	})
}

func (r *Routes) getCut(c *gin.Context) {
	id := c.Param("id")

	cut, err := r.executor.GetHistory().LoadCut(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cut not found"})
		return
	}

	c.JSON(http.StatusOK, cut)
}

func (r *Routes) getStats(c *gin.Context) {
	stats, err := r.executor.GetHistory().GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := &StatsResponse{
		TotalCuts:   stats.TotalCuts,
		SuccessCuts: stats.SuccessCuts,
		FailedCuts:  stats.FailedCuts,
		ByNode:      stats.ByNode,
		ByAction:    stats.ByAction,
		Nodes:       make(map[string]NodeStatsDetail),
	}

	if stats.TotalCuts > 0 {
		response.SuccessRate = float64(stats.SuccessCuts) / float64(stats.TotalCuts) * 100
	}

	if stats.FirstCut != nil {
		firstCut := stats.FirstCut.Format("2006-01-02T15:04:05Z")
		response.FirstCut = &firstCut
	}

	if stats.LastCut != nil {
		lastCut := stats.LastCut.Format("2006-01-02T15:04:05Z")
		response.LastCut = &lastCut
	}

	response.TotalDuration = int64(stats.TotalDuration.Seconds())

	for node, nodeStats := range stats.Nodes {
		response.Nodes[node] = NodeStatsDetail{
			TotalCuts: nodeStats.TotalCuts,
			Success:   nodeStats.Success,
			Failed:    nodeStats.Failed,
		}
	}

	c.JSON(http.StatusOK, response)
}

func (r *Routes) getNodeStats(c *gin.Context) {
	node := c.Param("node")

	trend, err := r.analyzer.GetNodeTrends(node)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, trend)
}

func (r *Routes) getTrends(c *gin.Context) {
	daysStr := c.DefaultQuery("days", "30")
	days, _ := strconv.Atoi(daysStr)

	trends, err := r.analyzer.GetGlobalTrends(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, trends)
}

func (r *Routes) getNodeTrends(c *gin.Context) {
	node := c.Param("node")

	trend, err := r.analyzer.GetNodeTrends(node)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, trend)
}

type DryRunRequest struct {
	Node    string  `json:"node" binding:"required"`
	Entropy float64 `json:"entropy" binding:"required,gte=0,lte=1"`
}

type DryRunResponse struct {
	Node         string  `json:"node"`
	Entropy      float64 `json:"entropy"`
	Action       string  `json:"action"`
	WouldExecute bool    `json:"would_execute"`
	Threshold    float64 `json:"threshold"`
	Critical     bool    `json:"critical"`
}

func (r *Routes) handleDryRun(c *gin.Context) {
	var req DryRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	policy := r.executor.GetPolicy()
	if policy == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Policy not available"})
		return
	}

	nodePolicy, ok := policy.GetNode(req.Node)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Node not found"})
		return
	}

	strategy, ok := nodePolicy.SelectStrategy(req.Entropy)
	if !ok {
		c.JSON(http.StatusOK, DryRunResponse{
			Node:         req.Node,
			Entropy:      req.Entropy,
			Action:       "none",
			WouldExecute: false,
		})
		return
	}

	c.JSON(http.StatusOK, DryRunResponse{
		Node:         req.Node,
		Entropy:      req.Entropy,
		Action:       strategy.Action,
		WouldExecute: true,
		Threshold:    strategy.Threshold,
		Critical:     strategy.Critical,
	})
}

func (r *Routes) exportCSV(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "1000")
	limit, _ := strconv.Atoi(limitStr)

	cuts, err := r.executor.GetHistory().ListCuts(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=cut_history.csv")

	csv := "ID,Node,Entropy,Action,Success,Error,LatencyMs,Timestamp\n"
	for _, cut := range cuts {
		csv += cut.ID + ","
		csv += cut.Node + ","
		csv += strconv.FormatFloat(cut.Entropy, 'f', 4, 64) + ","
		csv += cut.Action + ","
		csv += strconv.FormatBool(cut.Success) + ","
		csv += cut.Error + ","
		csv += strconv.FormatInt(cut.LatencyMs, 10) + ","
		csv += cut.Timestamp.Format("2006-01-02T15:04:05Z") + "\n"
	}

	c.String(http.StatusOK, csv)
}

func (r *Routes) exportJSON(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "1000")
	limit, _ := strconv.Atoi(limitStr)

	cuts, err := r.executor.GetHistory().ListCuts(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=cut_history.json")

	c.JSON(http.StatusOK, gin.H{
		"exported_at": exportTimestamp(),
		"total_cuts":  len(cuts),
		"cuts":        cuts,
	})
}

func (r *Routes) exportHTMLReport(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "1000")
	limit, _ := strconv.Atoi(limitStr)

	cuts, err := r.executor.GetHistory().ListCuts(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	stats, err := r.executor.GetHistory().GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/html")
	c.Header("Content-Disposition", "attachment; filename=remediation_report.html")

	successRate := 0.0
	if stats.TotalCuts > 0 {
		successRate = float64(stats.SuccessCuts) / float64(stats.TotalCuts) * 100
	}

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Atropos Remediation Report</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f0f0f5; color: #1a1a2e; padding: 2rem; }
        .container { max-width: 1200px; margin: 0 auto; }
        header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 2rem; border-radius: 8px; margin-bottom: 2rem; }
        h1 { margin-bottom: 0.5rem; }
        .meta { opacity: 0.9; font-size: 0.9rem; }
        .section { background: white; padding: 1.5rem; border-radius: 8px; margin-bottom: 1.5rem; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        h2 { color: #1a1a2e; margin-bottom: 1rem; border-bottom: 2px solid #667eea; padding-bottom: 0.5rem; }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
        .stat-card { background: #f8f9fa; padding: 1rem; border-radius: 6px; text-align: center; }
        .stat-value { font-size: 2rem; font-weight: 700; color: #667eea; }
        .stat-label { color: #6c757d; font-size: 0.85rem; text-transform: uppercase; }
        table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
        th, td { padding: 0.75rem; text-align: left; border-bottom: 1px solid #dee2e6; }
        th { background: #e9ecef; font-weight: 600; }
        .success { color: #28a745; }
        .failure { color: #dc3545; }
        .badge { padding: 0.25rem 0.5rem; border-radius: 4px; font-size: 0.85rem; }
        .badge.success { background: #d4edda; color: #155724; }
        .badge.failure { background: #f8d7da; color: #721c24; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Atropos Remediation Report</h1>
            <div class="meta">Generated on ` + exportTimestamp() + `</div>
        </header>

        <div class="section">
            <h2>Summary</h2>
            <div class="stats-grid">
                <div class="stat-card">
                    <div class="stat-value">` + strconv.Itoa(stats.TotalCuts) + `</div>
                    <div class="stat-label">Total Cuts</div>
                </div>
                <div class="stat-card">
                    <div class="stat-value success">` + strconv.Itoa(stats.SuccessCuts) + `</div>
                    <div class="stat-label">Successful</div>
                </div>
                <div class="stat-card">
                    <div class="stat-value failure">` + strconv.Itoa(stats.FailedCuts) + `</div>
                    <div class="stat-label">Failed</div>
                </div>
                <div class="stat-card">
                    <div class="stat-value">` + strconv.FormatFloat(successRate, 'f', 1, 64) + `%</div>
                    <div class="stat-label">Success Rate</div>
                </div>
            </div>
        </div>

        <div class="section">
            <h2>Cut History</h2>
            <table>
                <thead>
                    <tr>
                        <th>Timestamp</th>
                        <th>Node</th>
                        <th>Action</th>
                        <th>Entropy</th>
                        <th>Status</th>
                        <th>Latency</th>
                    </tr>
                </thead>
                <tbody>
`

	for _, cut := range cuts {
		statusBadge := `<span class="badge success">Success</span>`
		if !cut.Success {
			statusBadge = `<span class="badge failure">Failed</span>`
		}

		html += `
                    <tr>
                        <td>` + cut.Timestamp.Format("2006-01-02 15:04:05") + `</td>
                        <td>` + cut.Node + `</td>
                        <td>` + cut.Action + `</td>
                        <td>` + strconv.FormatFloat(cut.Entropy, 'f', 4, 64) + `</td>
                        <td>` + statusBadge + `</td>
                        <td>` + strconv.FormatInt(cut.LatencyMs, 10) + `ms</td>
                    </tr>`
	}

	html += `
                </tbody>
            </table>
        </div>

        <div class="section">
            <h2>By Node</h2>
            <table>
                <thead>
                    <tr>
                        <th>Node</th>
                        <th>Total Cuts</th>
                        <th>Success</th>
                        <th>Failed</th>
                    </tr>
                </thead>
                <tbody>
`

	for nodeId, nodeStats := range stats.Nodes {
		html += `
                    <tr>
                        <td>` + nodeId + `</td>
                        <td>` + strconv.Itoa(nodeStats.TotalCuts) + `</td>
                        <td class="success">` + strconv.Itoa(nodeStats.Success) + `</td>
                        <td class="failure">` + strconv.Itoa(nodeStats.Failed) + `</td>
                    </tr>`
	}

	html += `
                </tbody>
            </table>
        </div>

        <div class="section">
            <h2>By Action</h2>
            <table>
                <thead>
                    <tr>
                        <th>Action</th>
                        <th>Count</th>
                    </tr>
                </thead>
                <tbody>
`

	for action, count := range stats.ByAction {
		html += `
                    <tr>
                        <td>` + action + `</td>
                        <td>` + strconv.Itoa(count) + `</td>
                    </tr>`
	}

	html += `
                </tbody>
            </table>
        </div>
    </div>
</body>
</html>`

	c.String(http.StatusOK, html)
}

func (r *Routes) importClothoReport(c *gin.Context) {
	importer := correlation.NewClothoImporter()

	report, err := importer.ImportReport(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse Clotho report: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Clotho report imported successfully",
		"audit_id":       report.AuditID,
		"nodes":          report.Nodes,
		"findings_count": len(report.Findings),
	})
}

func (r *Routes) getCorrelation(c *gin.Context) {
	node := c.Param("node")
	hoursStr := c.DefaultQuery("hours", "24")
	hours, _ := strconv.Atoi(hoursStr)

	timeWindow := time.Duration(hours) * time.Hour

	importer := correlation.NewClothoImporter()

	cuts, err := r.executor.GetHistory().ListCutsByNode(node, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var cutRefs []correlation.CutReference
	for _, cut := range cuts {
		cutRefs = append(cutRefs, correlation.CutReference{
			ID:        cut.ID,
			Timestamp: cut.Timestamp,
			Action:    cut.Action,
			Success:   cut.Success,
		})
	}

	correlator := correlation.NewCorrelator(importer, cutRefs)

	result, err := correlator.Correlate(node, timeWindow)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	triggeringControls, err := correlator.GetTriggeringControls(node)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"node":                node,
		"time_window_hours":   hours,
		"effectiveness":       result.Effectiveness,
		"total_findings":      len(result.Findings),
		"remediated":          len(result.Remediated),
		"unresolved":          len(result.Unresolved),
		"triggering_controls": triggeringControls,
		"remediations":        result.Remediated,
		"unresolved_finding":  result.Unresolved,
	})
}

func exportTimestamp() string {
	return time.Now().Format("2006-01-02T15:04:05Z")
}

func (r *Routes) serveDashboard(c *gin.Context) {
	c.Redirect(http.StatusMovedPermanently, "/static/index.html")
}
