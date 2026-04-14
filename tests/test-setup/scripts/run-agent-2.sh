#!/bin/bash
# =============================================================================
# Run Agent 2 with HTTP Transport
# This agent runs in agent-cluster-2 and connects to broker via HTTP
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$SCRIPT_DIR/../../.."
AGENT_DIR="$ROOT_DIR/resource-agent"
CERT_DIR="$SCRIPT_DIR/../certs/agent2"
KUBECONFIGS_DIR="$SCRIPT_DIR/../kubeconfigs"

AGENT_ID="agent-cluster-2"
BROKER_URL="https://localhost:8443"

echo "=============================================="
echo "  Starting Liqo Resource Agent 2"
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

# Switch to agent cluster context
echo ""
echo "[1/3] Switching to agent-cluster-2 context..."
kubectl config use-context kind-agent-cluster-2

# Install CRDs in agent cluster
echo ""
echo "[2/3] Installing CRDs..."
cd "$AGENT_DIR"
make install 2>/dev/null || {
    echo "  -> Running 'make install' to create CRDs..."
    make install
}

# Build the agent
echo ""
echo "[3/3] Building and running agent..."
go build -o bin/agent ./cmd/main.go

echo ""
echo "=============================================="
echo "  Agent 2 Starting..."
echo "=============================================="
echo ""
echo "Configuration:"
echo "  Cluster ID:    $AGENT_ID"
echo "  Transport:     HTTP"
echo "  Broker URL:    $BROKER_URL"
echo "  Cert Path:     $CERT_DIR"
echo "  Kubeconfigs:   $KUBECONFIGS_DIR"
echo "  Adv Interval:  10s (publish to broker)"
echo "  Instr Poll:    5s (provider instructions)"
echo ""
echo "Press Ctrl+C to stop"
echo ""

# Run agent with HTTP transport
./bin/agent \
    --broker-transport=http \
    --broker-url="$BROKER_URL" \
    --broker-cert-path="$CERT_DIR" \
    --cluster-id="$AGENT_ID" \
    --advertisement-name="cluster-advertisement" \
    --advertisement-namespace=default \
    --health-probe-bind-address=:8083 \
    --metrics-bind-address=0 \
    --advertisement-requeue-interval=10s \
    --kubeconfigs-dir="$KUBECONFIGS_DIR"
