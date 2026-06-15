package metrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ScrapeGatewayLatency queries the Liqo gateway pod's Prometheus /metrics endpoint
// to retrieve the actual RTT latency (liqo_peer_latency_us) for a given remote cluster.
// The latency is returned in milliseconds. If scraping fails, an error is returned.
func ScrapeGatewayLatency(ctx context.Context, k8sClient client.Client, remoteClusterID string) (float64, error) {
	// The gateway pod lives in the tenant namespace for the remote cluster
	namespace := fmt.Sprintf("liqo-tenant-%s", remoteClusterID)

	// List pods in the tenant namespace to find the gateway pod
	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.InNamespace(namespace)); err != nil {
		return 0, fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
	}

	// Find the gateway pod (name starts with "gw-")
	var gwPod *corev1.Pod
	for i := range podList.Items {
		if strings.HasPrefix(podList.Items[i].Name, "gw-") {
			gwPod = &podList.Items[i]
			break
		}
	}

	if gwPod == nil {
		return 0, fmt.Errorf("no gateway pod found in namespace %s", namespace)
	}

	if gwPod.Status.PodIP == "" {
		return 0, fmt.Errorf("gateway pod %s has no IP address", gwPod.Name)
	}

	// Try common Liqo metrics ports
	metricsPorts := []int{5872, 8080, 9090}

	// Also check if any container exposes a named "metrics" port
	for _, container := range gwPod.Spec.Containers {
		for _, port := range container.Ports {
			if strings.Contains(strings.ToLower(port.Name), "metric") {
				// Prepend so it's tried first
				metricsPorts = append([]int{int(port.ContainerPort)}, metricsPorts...)
			}
		}
	}

	var lastErr error
	for _, port := range metricsPorts {
		latencyMs, err := scrapeMetricsEndpoint(ctx, gwPod.Status.PodIP, port)
		if err == nil {
			return latencyMs, nil
		}
		lastErr = err
	}

	return 0, fmt.Errorf("failed to scrape metrics from gateway pod %s on any port: %w", gwPod.Name, lastErr)
}

// scrapeMetricsEndpoint fetches /metrics from the given IP:port and extracts liqo_peer_latency_us.
// Returns the latency in milliseconds.
func scrapeMetricsEndpoint(ctx context.Context, podIP string, port int) (float64, error) {
	url := fmt.Sprintf("http://%s:%d/metrics", podIP, port)

	httpClient := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch metrics from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("metrics endpoint %s returned status %d", url, resp.StatusCode)
	}

	return parseLatencyMetric(resp.Body)
}

// parseLatencyMetric reads Prometheus text format and extracts the liqo_peer_latency_us value.
// Converts microseconds to milliseconds.
func parseLatencyMetric(body io.Reader) (float64, error) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// Look for liqo_peer_latency_us metric
		if strings.HasPrefix(line, "liqo_peer_latency_us") {
			// Format: liqo_peer_latency_us{labels...} <value>
			// or:     liqo_peer_latency_us <value>
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}

			valueStr := parts[len(parts)-1]
			latencyUs, err := strconv.ParseFloat(valueStr, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse latency value %q: %w", valueStr, err)
			}

			// Convert microseconds to milliseconds
			latencyMs := latencyUs / 1000.0
			return latencyMs, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading metrics: %w", err)
	}

	return 0, fmt.Errorf("liqo_peer_latency_us metric not found in response")
}
