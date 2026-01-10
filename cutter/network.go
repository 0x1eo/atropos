package cutter

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"atropos/internal/logger"
)

type NetworkCutter struct{}

func NewNetworkCutter() *NetworkCutter {
	return &NetworkCutter{}
}

func (n *NetworkCutter) Name() string {
	return "network"
}

func (n *NetworkCutter) CanHandle(action string) bool {
	return strings.HasPrefix(action, "ssh_")
}

func (n *NetworkCutter) Execute(ctx context.Context, target string, params map[string]string) error {
	host := params["host"]
	user := params["user"]
	if user == "" {
		user = "root"
	}
	port := params["port"]
	if port == "" {
		port = "22"
	}
	command := params["command"]

	if host == "" {
		return fmt.Errorf("network cutter requires host for target %s", target)
	}
	if command == "" {
		return fmt.Errorf("network cutter requires command")
	}

	logger.Get().Info("network_cut",
		zap.String("target", target),
		zap.String("host", host),
		zap.String("command", command),
	)

	client, err := n.connect(user, host, port)
	if err != nil {
		return fmt.Errorf("ssh connect: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	doneCh := make(chan error, 1)
	go func() {
		output, err := session.CombinedOutput(command)
		if err != nil {
			doneCh <- fmt.Errorf("command failed: %w, output: %s", err, string(output))
			return
		}
		doneCh <- nil
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-doneCh:
		return err
	}
}

func (n *NetworkCutter) connect(user, host, port string) (*ssh.Client, error) {
	authMethods := []ssh.AuthMethod{}

	if agentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		agentClient := agent.NewClient(agentConn)
		authMethods = append(authMethods, ssh.PublicKeysCallback(agentClient.Signers))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH auth available; start ssh-agent")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	return ssh.Dial("tcp", net.JoinHostPort(host, port), config)
}
