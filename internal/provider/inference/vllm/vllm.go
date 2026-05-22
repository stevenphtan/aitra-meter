package vllm

import (
	"context"
	"fmt"

	"github.com/aitra-ai/aitra-meter/internal/provider"
)

func init() {
	provider.RegisterInference("vllm", func(config map[string]string) (provider.InferenceMetricsProvider, error) {
		endpoint, ok := config["endpoint"]
		if !ok {
			endpoint = "http://localhost:8000/metrics"
		}
		return &VLLMProvider{endpoint: endpoint}, nil
	})
}

// VLLMProvider implements provider.InferenceMetricsProvider using vLLM's Prometheus /metrics endpoint.
type VLLMProvider struct {
	endpoint string
}

func (v *VLLMProvider) Name() string { return "vllm" }

func (v *VLLMProvider) OutputTokens(ctx context.Context) (uint64, error) {
	// TODO: scrape vllm:generation_tokens_total from v.endpoint
	return 0, fmt.Errorf("not implemented")
}

func (v *VLLMProvider) RequestsRunning(ctx context.Context) (int, error) {
	// TODO: scrape vllm:num_requests_running from v.endpoint
	return 0, fmt.Errorf("not implemented")
}

func (v *VLLMProvider) ModelName(ctx context.Context) (string, error) {
	// TODO: read model_name label from vLLM metrics
	return "", fmt.Errorf("not implemented")
}
