package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

func main() {
	metricsAddr := flag.String("metrics-addr",  ":8080", "Prometheus metrics and API listen address")
	grpcAddr    := flag.String("grpc-addr",     ":9091", "gRPC listen address for measurement agents")
	clusterName := flag.String("cluster",       "",      "Cluster name (required)")
	logLevel    := flag.String("log-level",     "info",  "Log level: debug | info | warn | error")
	flag.Parse()

	if *clusterName == "" {
		*clusterName = os.Getenv("CLUSTER_NAME")
	}

	log := newLogger(*logLevel)
	defer log.Sync() //nolint:errcheck

	log.Info("starting aggregation service",
		zap.String("cluster", *clusterName),
		zap.String("metrics_addr", *metricsAddr),
		zap.String("grpc_addr", *grpcAddr),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// TODO: start gRPC server, aggregation loop, ClickHouse writer
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	srv := &http.Server{Addr: *metricsAddr, Handler: mux}
	go func() {
		log.Info("metrics server listening", zap.String("addr", *metricsAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("metrics server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	_ = srv.Shutdown(context.Background())
}

func newLogger(level string) *zap.Logger {
	cfg := zap.NewProductionConfig()
	_ = cfg.Level.UnmarshalText([]byte(level))
	l, _ := cfg.Build()
	return l
}
