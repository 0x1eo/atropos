package cutter

import "context"

type Cutter interface {
	Name() string
	CanHandle(action string) bool
	Execute(ctx context.Context, target string, params map[string]string) error
}

type CutResult struct {
	Target    string
	Action    string
	Success   bool
	Error     error
	LatencyMs int64
}

type Registry struct {
	cutters []Cutter
}

func NewRegistry() *Registry {
	return &Registry{
		cutters: []Cutter{
			NewDockerCutter(),
			NewNetworkCutter(),
			NewVBoxCutter(),
		},
	}
}

func (r *Registry) FindCutter(action string) (Cutter, bool) {
	for _, c := range r.cutters {
		if c.CanHandle(action) {
			return c, true
		}
	}
	return nil, false
}

func (r *Registry) Register(c Cutter) {
	r.cutters = append(r.cutters, c)
}
