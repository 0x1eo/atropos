package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"atropos/api"
	"atropos/engine"
	"atropos/internal/logger"
	"atropos/policy"
)

func main() {
	policyPath := flag.String("policy", "atropos_policy.yaml", "Path to policy file")
	flag.Parse()

	log := logger.Get()
	log.Info("ATROPOS_INIT", zap.String("policy_file", *policyPath))

	pol, err := policy.LoadPolicy(*policyPath)
	if err != nil {
		log.Fatal("POLICY_LOAD_FAILED", zap.Error(err))
	}

	log.Info("POLICY_LOADED", zap.Int("node_count", len(pol.Nodes)))

	exec := engine.NewExecutor(pol)
	server := api.NewServer(exec, pol.GetHMACSecret())

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Info("ATROPOS_SHUTDOWN")
		os.Exit(0)
	}()

	addr := pol.GetListenAddr()
	log.Info("ATROPOS_ONLINE", zap.String("listen_addr", addr))

	if err := server.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
