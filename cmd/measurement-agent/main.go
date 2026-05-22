package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	// Import providers to trigger their init() registration.
	_ "github.com/aitra-ai/aitra-meter/internal/provider/energy/zeus"
	_ "github.com/aitra-ai/aitra-meter/internal/provider/energy/nvml"
	_ "github.com/aitra-ai/aitra-meter/internal/provider/inference/vllm"
	_ "github.com/aitra-ai/aitra-meter/internal/provider/inference/genericprometheus"
)

func main() {
	energyType    := flag.String("energy-provider",    "zeus", "Energy provider: zeus | nvml")
	inferenceType := flag.String("inference-provider", "vllm", "Inference provider: vllm | generic-prometheus")
	aggregatorAddr := flag.String("aggregator",        "aitra-meter-aggregation:9091", "Aggregation service gRPC address")
	logLevel       := flag.String("log-level",         "info",  "Log level: debug | info | warn | error")
	flag.Parse()

	log := newLogger(*logLevel)
	defer log.Sync() //nolint:errcheck

	log.Info("starting measurement agent",
		zap.String("energy_provider", *energyType),
		zap.String("inference_provider", *inferenceType),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// TODO: initialize providers, start measurement loop, stream to aggregator
	<-ctx.Done()
	log.Info("shutting down")
}

func newLogger(level string) *zap.Logger {
	cfg := zap.NewProductionConfig()
	_ = cfg.Level.UnmarshalText([]byte(level))
	l, _ := cfg.Build()
	return l
}
