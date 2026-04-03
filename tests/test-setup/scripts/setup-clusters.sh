#!/bin/bash
# =============================================================================
# Setup Kind Clusters for Testing
# Creates: 1 broker cluster + 2 agent clusters
# Exports kubeconfigs and installs Liqo on agent clusters
# =============================================================================

set -e

echo "=============================================="
echo "  Creating Kind Clusters for Testing"
echo "=============================================="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KUBECONFIGS_DIR="$SCRIPT_DIR/../kubeconfigs"

# Cluster names
BROKER_CLUSTER="broker-cluster"
AGENT1_CLUSTER="agent-cluster-1"
AGENT2_CLUSTER="agent-cluster-2"

# Check if kind is installed
if ! command -v kind &> /dev/null; then
    echo "Error: kind is not installed"
    echo "Install it with: brew install kind"
    exit 1
fi

# Create broker cluster
echo ""
echo "[1/3] Creating broker cluster: $BROKER_CLUSTER"
if kind get clusters | grep -q "^${BROKER_CLUSTER}$"; then
    echo "  -> Cluster already exists, skipping..."
else
    kind create cluster --name $BROKER_CLUSTER --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30443
    hostPort: 8443
    protocol: TCP
EOF
    echo "  -> Created!"
fi

# Create agent cluster 1
echo ""
echo "[2/3] Creating agent cluster: $AGENT1_CLUSTER"
if kind get clusters | grep -q "^${AGENT1_CLUSTER}$"; then
    echo "  -> Cluster already exists, skipping..."
else
    kind create cluster --name $AGENT1_CLUSTER
    echo "  -> Created!"
fi

# Create agent cluster 2
echo ""
echo "[3/3] Creating agent cluster: $AGENT2_CLUSTER"
if kind get clusters | grep -q "^${AGENT2_CLUSTER}$"; then
    echo "  -> Cluster already exists, skipping..."
else
    kind create cluster --name $AGENT2_CLUSTER
    echo "  -> Created!"
fi

# =============================================================================
# Export kubeconfigs for Liqo peering
# =============================================================================
echo ""
echo "=============================================="
echo "  Exporting Kubeconfigs"
echo "=============================================="

mkdir -p "$KUBECONFIGS_DIR"

echo ""
echo "Exporting kubeconfig for $AGENT1_CLUSTER..."
kind get kubeconfig --name $AGENT1_CLUSTER > "$KUBECONFIGS_DIR/$AGENT1_CLUSTER.kubeconfig"
echo "  -> $KUBECONFIGS_DIR/$AGENT1_CLUSTER.kubeconfig"

echo "Exporting kubeconfig for $AGENT2_CLUSTER..."
kind get kubeconfig --name $AGENT2_CLUSTER > "$KUBECONFIGS_DIR/$AGENT2_CLUSTER.kubeconfig"
echo "  -> $KUBECONFIGS_DIR/$AGENT2_CLUSTER.kubeconfig"

# =============================================================================
# Install Liqo on agent clusters
# =============================================================================
echo ""
echo "=============================================="
echo "  Installing Liqo on Agent Clusters"
echo "=============================================="

if ! command -v liqoctl &> /dev/null; then
    echo ""
    echo "WARNING: liqoctl is not installed!"
    echo "Liqo peering will not work without it."
    echo ""
    echo "Install liqoctl:"
    echo "  curl --fail -LS https://get.liqo.io | bash"
    echo ""
    echo "Skipping Liqo installation..."
else
    echo ""
    echo "[1/2] Installing Liqo on $AGENT1_CLUSTER..."
    
    # [NOTE]
    # The original code has the line (that is still kept): liqoctl install --kubeconfig ... --cluster-name $AGENT1_CLUSTER
    # In Liqo v1.1.2 the flag "--cluster-name" is removved and the installation on "kind" 
    # requires the specific command "install kind". This is the correct version to uncomment:
    # 
    # liqoctl install kind --cluster-id $AGENT1_CLUSTER --kubeconfig "$KUBECONFIGS_DIR/$AGENT1_CLUSTER.kubeconfig" 2>&1 | tail -5
    
    liqoctl install --kubeconfig "$KUBECONFIGS_DIR/$AGENT1_CLUSTER.kubeconfig" --cluster-name $AGENT1_CLUSTER 2>&1 | tail -5
    echo "  -> Liqo installed on $AGENT1_CLUSTER"

    echo ""
    echo "[2/2] Installing Liqo on $AGENT2_CLUSTER..."
    
    # [NOTE] 
    # See comment above for the fix. Uncomment the line below to enable it:
    # liqoctl install kind --cluster-id $AGENT2_CLUSTER --kubeconfig "$KUBECONFIGS_DIR/$AGENT2_CLUSTER.kubeconfig" 2>&1 | tail -5
    
    liqoctl install --kubeconfig "$KUBECONFIGS_DIR/$AGENT2_CLUSTER.kubeconfig" --cluster-name $AGENT2_CLUSTER 2>&1 | tail -5
    echo "  -> Liqo installed on $AGENT2_CLUSTER"
fi

echo ""
echo "=============================================="
echo "  Setup Complete!"
echo "=============================================="
echo ""
echo "Available contexts:"
kubectl config get-contexts | grep kind
echo ""
echo "Kubeconfigs exported to: $KUBECONFIGS_DIR/"
echo ""
echo "Switch context with:"
echo "  kubectl config use-context kind-broker-cluster"
echo "  kubectl config use-context kind-agent-cluster-1"
echo "  kubectl config use-context kind-agent-cluster-2"
echo ""
