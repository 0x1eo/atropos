package cutter

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"

	"atropos/internal/logger"
)

type VBoxCutter struct{}

func NewVBoxCutter() *VBoxCutter {
	return &VBoxCutter{}
}

func (v *VBoxCutter) Name() string {
	return "vbox"
}

func (v *VBoxCutter) CanHandle(action string) bool {
	return strings.HasPrefix(action, "vbox_")
}

func (v *VBoxCutter) Execute(ctx context.Context, target string, params map[string]string) error {
	action := params["action"]
	vmName := params["vm_name"]
	if vmName == "" {
		vmName = target
	}

	logger.Get().Info("vbox_cut",
		zap.String("target", target),
		zap.String("vm", vmName),
		zap.String("action", action),
	)

	switch action {
	case "vbox_revert_snapshot":
		snapshotName := params["snapshot_name"]
		if snapshotName == "" {
			return fmt.Errorf("vbox_revert_snapshot requires snapshot_name")
		}
		return v.revertSnapshot(ctx, vmName, snapshotName)
	case "vbox_poweroff":
		return v.powerOff(ctx, vmName)
	case "vbox_reset":
		return v.reset(ctx, vmName)
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}
}

func (v *VBoxCutter) revertSnapshot(ctx context.Context, vmName, snapshotName string) error {
	_ = v.powerOff(ctx, vmName)

	cmd := exec.CommandContext(ctx, "VBoxManage", "snapshot", vmName, "restore", snapshotName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restore snapshot %q: %w, output: %s", snapshotName, err, string(output))
	}

	startCmd := exec.CommandContext(ctx, "VBoxManage", "startvm", vmName, "--type", "headless")
	if output, err := startCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start VM: %w, output: %s", err, string(output))
	}

	return nil
}

func (v *VBoxCutter) powerOff(ctx context.Context, vmName string) error {
	cmd := exec.CommandContext(ctx, "VBoxManage", "controlvm", vmName, "poweroff")
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "not currently running") {
		return fmt.Errorf("poweroff: %w, output: %s", err, string(output))
	}
	return nil
}

func (v *VBoxCutter) reset(ctx context.Context, vmName string) error {
	cmd := exec.CommandContext(ctx, "VBoxManage", "controlvm", vmName, "reset")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reset: %w, output: %s", err, string(output))
	}
	return nil
}
