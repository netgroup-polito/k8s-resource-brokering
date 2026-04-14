#!/bin/bash
# =============================================================================
# Run Broker with HTTP Interface
# This script runs the broker locally connected to the broker-cluster
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$SCRIPT_DIR/../../.."
BROKER_DIR="$ROOT_DIR/resource-broker"
CERT_DIR="$SCRIPT_DIR/../certs/broker"

echo "=============================================="
echo "  Starting Liqo Resource Broker"
echo "=============================================="

# Check if certificates exist
if [ ! -f "$CERT_DIR/tls.crt" ]; then
    echo "Error: Certificates not found at $CERT_DIR"
    echo ""
    echo "Run these scripts first:"
    echo "  1. bash scripts/setup-clusters.sh"
    echo "  2. bash scripts/setup-certmanager.sh"
    echo "  3. bash scripts/extract-certs.sh"
    exit 1
fi

# Switch to broker cluster context
echo ""
echo "[1/3] Switching to broker-cluster context..."
kubectl config use-context kind-broker-cluster

# Install CRDs in broker cluster
echo ""
echo "[2/3] Installing CRDs..."
cd "$BROKER_DIR"
make install 2>/dev/null || {
    echo "  -> Running 'make install' to create CRDs..."
    make install
}

# Build the broker
echo ""
echo "[3/3] Building and running broker..."
go build -o bin/broker ./cmd/main.go

echo ""
echo "=============================================="
echo "  Broker Starting..."
echo "=============================================="
echo ""
echo "Configuration:"
echo "  Interface:  HTTP (REST API with mTLS)"
echo "  Port:       8443"
echo "  Cert Path:  $CERT_DIR"
echo "  Namespace:  default"
echo ""
echo "Press Ctrl+C to stop"
echo ""

# Run broker with HTTP interface
./bin/broker \
    --broker-interface=http \
    --http-port=8443 \
    --http-cert-path="$CERT_DIR" \
    --http-namespace=default \
    --health-probe-bind-address=:8081 \
    --metrics-bind-address=0
