package cutter

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"go.uber.org/zap"

	"atropos/internal/logger"
)

type DockerCutter struct {
	cli *client.Client
}

func NewDockerCutter() *DockerCutter {
	return &DockerCutter{}
}

func (d *DockerCutter) Name() string {
	return "docker"
}

func (d *DockerCutter) CanHandle(action string) bool {
	return strings.HasPrefix(action, "docker_")
}

func (d *DockerCutter) Execute(ctx context.Context, target string, params map[string]string) error {
	if d.cli == nil {
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			return fmt.Errorf("docker client: %w", err)
		}
		d.cli = cli
	}

	action := params["action"]
	logger.Get().Info("docker_cut",
		zap.String("target", target),
		zap.String("action", action),
	)

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("atropos.node=%s", target))

	containers, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	if len(containers) == 0 {
		containers, err = d.cli.ContainerList(ctx, container.ListOptions{All: false})
		if err != nil {
			return fmt.Errorf("list all containers: %w", err)
		}
	}

	for _, c := range containers {
		var opErr error
		switch action {
		case "docker_pause_all":
			if c.State == "running" {
				opErr = d.cli.ContainerPause(ctx, c.ID)
			}
		case "docker_stop_all":
			opErr = d.cli.ContainerStop(ctx, c.ID, container.StopOptions{})
		case "docker_kill_all":
			opErr = d.cli.ContainerKill(ctx, c.ID, "SIGKILL")
		default:
			return fmt.Errorf("unsupported action: %s", action)
		}

		if opErr != nil {
			logger.CutFailed(target, action, opErr)
			return fmt.Errorf("%s container %s: %w", action, c.ID[:12], opErr)
		}
	}

	return nil
}
