package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	measurementv1 "github.com/aitra-ai/aitra-meter/api/proto/measurement/v1"
	"github.com/aitra-ai/aitra-meter/internal/aggregation"
	"github.com/aitra-ai/aitra-meter/internal/clickhouse"
)

func main() {
	metricsAddr  := flag.String("metrics-addr",   ":8080", "Prometheus metrics and API listen address")
	grpcAddr     := flag.String("grpc-addr",      ":9091", "gRPC listen address for measurement agents")
	clusterName  := flag.String("cluster",        "",      "Cluster name (required)")
	clickhouseDSN := flag.String("clickhouse-dsn", "",     "ClickHouse DSN (omit to disable persistence)")
	logLevel     := flag.String("log-level",      "info",  "Log level: debug | info | warn | error")
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

	// --- ClickHouse writer -------------------------------------------------
	var writer aggregation.RecordWriter = &noopRecordWriter{log: log}
	if *clickhouseDSN != "" {
		chWriter, err := clickhouse.New(ctx, clickhouse.Config{DSN: *clickhouseDSN}, log)
		if err != nil {
			log.Fatal("clickhouse init failed", zap.Error(err))
		}
		defer chWriter.Close(context.Background()) //nolint:errcheck
		writer = chWriter
		log.Info("clickhouse writer enabled")
	} else {
		log.Warn("--clickhouse-dsn not set; measurement records will not be persisted")
	}

	// --- aggregation loop --------------------------------------------------
	// PodLookup and NodeHardware stubs replaced in a subsequent PR (k8s client).
	loop := aggregation.NewLoop(
		*clusterName,
		aggregation.NewResolver(&noopPodLookup{}, aggregation.PolicyConfig{}),
		aggregation.NewCalibrationTableFromMap(nil),
		&noopNodeHardware{},
		writer,
	)

	// --- gRPC server -------------------------------------------------------
	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatal("gRPC listen failed", zap.String("addr", *grpcAddr), zap.Error(err))
	}
	grpcSrv := grpc.NewServer()
	measurementv1.RegisterMeasurementServiceServer(grpcSrv, loop)
	go func() {
		log.Info("gRPC server listening", zap.String("addr", *grpcAddr))
		if err := grpcSrv.Serve(lis); err != nil {
			log.Error("gRPC server stopped", zap.Error(err))
		}
	}()

	// --- HTTP server (metrics + health) ------------------------------------
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

	httpSrv := &http.Server{Addr: *metricsAddr, Handler: mux}
	go func() {
		log.Info("metrics server listening", zap.String("addr", *metricsAddr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("metrics server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	grpcSrv.GracefulStop()
	_ = httpSrv.Shutdown(context.Background())
}

func newLogger(level string) *zap.Logger {
	cfg := zap.NewProductionConfig()
	_ = cfg.Level.UnmarshalText([]byte(level))
	l, _ := cfg.Build()
	return l
}

// --- noop stubs (replaced in later PRs) ------------------------------------

type noopPodLookup struct{}

func (n *noopPodLookup) ByNodeAndModel(_ context.Context, _, _ string) (aggregation.PodMeta, error) {
	return aggregation.PodMeta{Namespace: "unknown", Workload: "unknown", Precision: "unknown"}, nil
}

type noopNodeHardware struct{}

func (n *noopNodeHardware) Hardware(_ context.Context, _ string) string { return "unknown" }

type noopRecordWriter struct{ log *zap.Logger }

func (n *noopRecordWriter) Write(_ context.Context, r aggregation.MeasurementRecord) error {
	n.log.Debug("measurement record (no clickhouse DSN configured)",
		zap.String("node", r.Node),
		zap.String("namespace", r.Namespace),
		zap.Float64("j_per_token", r.JPerToken),
	)
	return nil
}
