package policy

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

type Strategy struct {
	Threshold    float64 `yaml:"threshold"`
	Action       string  `yaml:"action"`
	Command      string  `yaml:"command,omitempty"`
	Critical     bool    `yaml:"critical,omitempty"`
	SnapshotName string  `yaml:"snapshot_name,omitempty"`
	EscalateTo   string  `yaml:"escalate_to,omitempty"`
	OnFailure    string  `yaml:"on_failure,omitempty"`
}

type TimeWindow struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type NodePolicy struct {
	Host        string       `yaml:"host,omitempty"`
	Port        int          `yaml:"port,omitempty"`
	User        string       `yaml:"user,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Strategies  []Strategy   `yaml:"strategies"`
	TimeWindows []TimeWindow `yaml:"time_windows,omitempty"`
	RateLimit   *RateLimit   `yaml:"rate_limit,omitempty"`
	Name        string       `yaml:"-"`
}

type RateLimit struct {
	MaxCuts int `yaml:"max_cuts"`
	Window  int `yaml:"window_minutes"`
}

type ServerConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	HMACSecret string `yaml:"hmac_secret"`
}

type Meta struct {
	Version      string `yaml:"version"`
	LastReviewed string `yaml:"last_reviewed"`
}

type RemediationPolicy struct {
	Meta      Meta                   `yaml:"meta"`
	Server    ServerConfig           `yaml:"server"`
	Nodes     map[string]*NodePolicy `yaml:"nodes"`
	nodeIndex map[string]*NodePolicy
}

func LoadPolicy(path string) (*RemediationPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}

	var policy RemediationPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}

	if err := policy.validate(); err != nil {
		return nil, err
	}

	policy.buildIndex()
	return &policy, nil
}

func (p *RemediationPolicy) validate() error {
	if len(p.Nodes) == 0 {
		return fmt.Errorf("policy must define at least one node")
	}

	for name, node := range p.Nodes {
		if len(node.Strategies) == 0 {
			return fmt.Errorf("node %q: needs at least one strategy", name)
		}
		for j, strat := range node.Strategies {
			if strat.Threshold < 0 || strat.Threshold > 1 {
				return fmt.Errorf("node %q strategy %d: threshold must be 0-1", name, j)
			}
			if strat.Action == "" {
				return fmt.Errorf("node %q strategy %d: action required", name, j)
			}
		}
	}

	return nil
}

func (p *RemediationPolicy) buildIndex() {
	p.nodeIndex = make(map[string]*NodePolicy, len(p.Nodes))
	for name, node := range p.Nodes {
		node.Name = name
		sort.Slice(node.Strategies, func(a, b int) bool {
			return node.Strategies[a].Threshold > node.Strategies[b].Threshold
		})
		p.nodeIndex[name] = node
	}
}

func (p *RemediationPolicy) GetNode(name string) (*NodePolicy, bool) {
	node, ok := p.nodeIndex[name]
	return node, ok
}

func (n *NodePolicy) SelectStrategy(entropy float64) (*Strategy, bool) {
	for i := range n.Strategies {
		if entropy >= n.Strategies[i].Threshold {
			return &n.Strategies[i], true
		}
	}
	return nil, false
}

func (n *NodePolicy) SelectStrategyByAction(action string) (*Strategy, bool) {
	for i := range n.Strategies {
		if n.Strategies[i].Action == action {
			return &n.Strategies[i], true
		}
	}
	return nil, false
}

func (n *NodePolicy) GetEscalationStrategy(currentThreshold float64) (*Strategy, bool) {
	for i := range n.Strategies {
		if n.Strategies[i].Threshold > currentThreshold {
			return &n.Strategies[i], true
		}
	}
	return nil, false
}

func (p *RemediationPolicy) GetListenAddr() string {
	if p.Server.ListenAddr != "" {
		return p.Server.ListenAddr
	}
	return ":8443"
}

func (p *RemediationPolicy) GetHMACSecret() string {
	if secret := os.Getenv("ATROPOS_HMAC_SECRET"); secret != "" {
		return secret
	}
	return p.Server.HMACSecret
}
