package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mehdiazizian/liqo-resource-agent/internal/transport/dto"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// HTTPCommunicator implements BrokerCommunicator interface using HTTP REST API
type HTTPCommunicator struct {
	httpClient *http.Client
	baseURL    string
	clusterID  string
	maxRetries int
}

// NewHTTPCommunicator creates a new HTTP-based broker communicator with mTLS
func NewHTTPCommunicator(brokerURL, certPath, clusterID string) (*HTTPCommunicator, error) {
	// Load client certificate (tls.crt, tls.key)
	cert, err := tls.LoadX509KeyPair(
		filepath.Join(certPath, "tls.crt"),
		filepath.Join(certPath, "tls.key"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate for server verification
	caCert, err := os.ReadFile(filepath.Join(certPath, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA certificate")
	}

	// Create TLS config with mTLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	// Create HTTP client with connection pooling
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        10,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &HTTPCommunicator{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		baseURL:    brokerURL,
		clusterID:  clusterID,
		maxRetries: 3,
	}, nil
}

// PublishAdvertisement publishes cluster advertisement to broker via HTTP.
// CRITICAL: Implements Reserved field preservation logic.
// Returns any piggybacked provider instructions from the broker response.
func (c *HTTPCommunicator) PublishAdvertisement(ctx context.Context, adv *dto.AdvertisementDTO) ([]*dto.ReservationDTO, error) {
	logger := log.FromContext(ctx).WithName("http-communicator")

	// STEP 1: Fetch existing advertisement to get Reserved field
	// This is CRITICAL to preserve broker's resource locking state
	existingURL := fmt.Sprintf("%s/api/v1/advertisements/%s", c.baseURL, adv.ClusterID)

	req, err := http.NewRequestWithContext(ctx, "GET", existingURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}

	resp, err := c.doWithRetry(ctx, req)
	if err == nil && resp.StatusCode == http.StatusOK {
		var existing dto.AdvertisementDTO
		if err := json.NewDecoder(resp.Body).Decode(&existing); err == nil {
			// CRITICAL: Preserve Reserved field from broker
			// The broker manages this field to track locked resources
			// Agent MUST NOT overwrite it or race conditions occur
			if existing.Resources.Reserved != nil {
				logger.Info("Preserving Reserved field from broker",
					"cpu", existing.Resources.Reserved.CPU,
					"memory", existing.Resources.Reserved.Memory)
				adv.Resources.Reserved = existing.Resources.Reserved
			}
		}
		resp.Body.Close()
	}

	// STEP 2: Publish advertisement with preserved Reserved field
	body, err := json.Marshal(adv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal advertisement: %w", err)
	}

	postURL := fmt.Sprintf("%s/api/v1/advertisements", c.baseURL)
	req, err = http.NewRequestWithContext(ctx, "POST", postURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = c.doWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to publish advertisement: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("broker returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// STEP 3: Parse response which includes provider instructions
	var advResponse dto.AdvertisementResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&advResponse); err != nil {
		// Non-fatal: advertisement was published, just can't parse provider instructions
		logger.Error(err, "Failed to decode advertisement response (advertisement was published)")
		return nil, nil
	}

	logger.Info("Advertisement published successfully",
		"clusterID", adv.ClusterID,
		"availableCPU", adv.Resources.Available.CPU,
		"availableMemory", adv.Resources.Available.Memory,
		"providerInstructions", len(advResponse.ProviderInstructions))

	return advResponse.ProviderInstructions, nil
}

// RequestReservation sends a synchronous reservation request to the broker.
// The broker runs its decision engine inline and returns the instruction
// in the response. No polling needed.
func (c *HTTPCommunicator) RequestReservation(ctx context.Context, reqDTO *dto.ReservationRequestDTO) (*dto.ReservationDTO, error) {
	logger := log.FromContext(ctx).WithName("http-communicator")

	body, err := json.Marshal(reqDTO)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reservation request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/reservations", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send reservation request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("broker returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var reservation dto.ReservationDTO
	if err := json.NewDecoder(resp.Body).Decode(&reservation); err != nil {
		return nil, fmt.Errorf("failed to decode reservation response: %w", err)
	}

	logger.Info("Reservation created synchronously",
		"reservationID", reservation.ID,
		"targetCluster", reservation.TargetClusterID,
		"cpu", reservation.RequestedResources.CPU,
		"memory", reservation.RequestedResources.Memory)

	return &reservation, nil
}

// FetchInstructions polls the broker for pending provider instructions.
// This is a lightweight GET request that returns near-instantly.
func (c *HTTPCommunicator) FetchInstructions(ctx context.Context) ([]*dto.ReservationDTO, error) {
	url := fmt.Sprintf("%s/api/v1/instructions", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch instructions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("broker returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var instructions []*dto.ReservationDTO
	if err := json.NewDecoder(resp.Body).Decode(&instructions); err != nil {
		return nil, fmt.Errorf("failed to decode instructions: %w", err)
	}

	return instructions, nil
}

// GetReservation fetches a specific reservation from the broker
func (c *HTTPCommunicator) GetReservation(ctx context.Context, reservationID string) (*dto.ReservationDTO, error) {
	url := fmt.Sprintf("%s/api/v1/reservations/%s", c.baseURL, reservationID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch reservation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("broker returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var reservation dto.ReservationDTO
	if err := json.NewDecoder(resp.Body).Decode(&reservation); err != nil {
		return nil, fmt.Errorf("failed to decode reservation: %w", err)
	}

	return &reservation, nil
}

// UploadPeeringKubeconfig securely sends the generated peering-user kubeconfig
// to the broker, so that the requester cluster can download and use it.
func (c *HTTPCommunicator) UploadPeeringKubeconfig(ctx context.Context, reservationID string, kubeconfig string) error {
	logger := log.FromContext(ctx).WithName("http-communicator")

	payload := struct {
		Kubeconfig string `json:"kubeconfig"`
	}{
		Kubeconfig: kubeconfig,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal kubeconfig payload: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/reservations/%s/kubeconfig", c.baseURL, reservationID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to upload peering kubeconfig: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("broker returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	logger.Info("Successfully uploaded peering kubeconfig", "reservationID", reservationID)
	return nil
}

// Ping checks connectivity to broker
func (c *HTTPCommunicator) Ping(ctx context.Context) error {
	url := fmt.Sprintf("%s/healthz", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("broker returned status %d", resp.StatusCode)
	}

	return nil
}

// Close cleans up resources
func (c *HTTPCommunicator) Close() error {
	// Close idle connections
	c.httpClient.CloseIdleConnections()
	return nil
}

// doWithRetry executes HTTP request with exponential backoff retry logic
func (c *HTTPCommunicator) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	backoff := 1 * time.Second
	maxBackoff := 16 * time.Second

	// Save body for retries (body can only be read once)
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		req.Body.Close()
	}

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Recreate body for each attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := c.httpClient.Do(req)

		// Success or non-retryable error
		if err == nil {
			// Retry on 5xx errors (server errors)
			if resp.StatusCode < 500 {
				return resp, nil
			}
			resp.Body.Close() // Close before retry
		}

		// Don't retry on last attempt
		if attempt == c.maxRetries {
			if err != nil {
				return nil, fmt.Errorf("max retries exceeded: %w", err)
			}
			return resp, nil // Return the 5xx response
		}

		// Wait before retry with exponential backoff
		select {
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("max retries exceeded")
}
