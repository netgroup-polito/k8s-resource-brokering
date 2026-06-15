package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/mehdiazizian/liqo-resource-broker/internal/api/handlers"
	"github.com/mehdiazizian/liqo-resource-broker/internal/api/middleware"
)

// Server wraps HTTP server for broker REST API
type Server struct {
	httpServer *http.Server
	handlers   *handlers.Handler
}

// NewServer creates a new HTTP REST API server with mTLS
func NewServer(port string, certPath string, handler *handlers.Handler) (*Server, error) {
	// Load server certificate
	cert, err := tls.LoadX509KeyPair(
		filepath.Join(certPath, "tls.crt"),
		filepath.Join(certPath, "tls.key"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	// Load CA certificate for client verification
	caCert, err := os.ReadFile(filepath.Join(certPath, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA certificate")
	}

	// mTLS configuration - require and verify client certificates
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	// Create router
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("POST /api/v1/advertisements", handler.PostAdvertisement)
	mux.HandleFunc("GET /api/v1/advertisements/{clusterID}", handler.GetAdvertisement)
	mux.HandleFunc("POST /api/v1/reservations", handler.PostReservation)
	mux.HandleFunc("POST /api/v1/evaluations", handler.PostEvaluation)
	mux.HandleFunc("GET /api/v1/reservations/{id}", handler.GetReservation)
	mux.HandleFunc("POST /api/v1/reservations/{id}/kubeconfig", handler.PostPeeringKubeconfig)
	mux.HandleFunc("GET /api/v1/instructions", handler.GetInstructions)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Apply middleware chain
	handlerWithMiddleware := middleware.Chain(
		mux,
		middleware.ValidateClientCertificate,
		middleware.Logging,
	)

	return &Server{
		httpServer: &http.Server{
			Addr:      ":" + port,
			Handler:   handlerWithMiddleware,
			TLSConfig: tlsConfig,
		},
		handlers: handler,
	}, nil
}

// Start begins serving HTTP requests
func (s *Server) Start() error {
	// Certificate already in TLSConfig, pass empty strings
	return s.httpServer.ListenAndServeTLS("", "")
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("Shutting down HTTP API server")
	return s.httpServer.Shutdown(ctx)
}
