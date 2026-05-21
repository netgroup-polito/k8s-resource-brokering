package certmanager

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/netgroup-polito/k8s-resource-brokering/tests/internal/cluster"
)

const (
	CertManagerVersion = "v1.14.0"
	CertManagerURL     = "https://github.com/cert-manager/cert-manager/releases/download/" + CertManagerVersion + "/cert-manager.yaml"
)

// SetupCertManager installs cert-manager in the broker cluster and creates all required certificates.
func SetupCertManager() error {
	fmt.Println("Installing cert-manager in broker cluster...")
	
	// Switch to broker cluster context
	if err := switchContext("kind-" + cluster.BrokerCluster); err != nil {
		return err
	}

	// Check if cert-manager namespace exists
	cmd := exec.Command("kubectl", "get", "namespace", "cert-manager")
	if err := cmd.Run(); err != nil {
		// Namespace doesn't exist, install cert-manager
		fmt.Println("Installing cert-manager...")
		cmd = exec.Command("kubectl", "apply", "-f", CertManagerURL)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to apply cert-manager manifests: %w", err)
		}

		fmt.Println("Waiting for cert-manager to be ready...")
		deployments := []string{"cert-manager", "cert-manager-webhook", "cert-manager-cainjector"}
		for _, dep := range deployments {
			cmd = exec.Command("kubectl", "wait", "--for=condition=Available", "deployment/"+dep, "-n", "cert-manager", "--timeout=300s")
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to wait for deployment %s: %w", dep, err)
			}
		}
		fmt.Println("cert-manager is ready!")
	} else {
		fmt.Println("cert-manager already installed, skipping...")
	}

	if err := createIssuersAndCertificates(); err != nil {
		return err
	}

	return nil
}

func createIssuersAndCertificates() error {
	fmt.Println("Creating CA Issuer and Certificates...")

	manifests := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: liqo-selfsigned-issuer
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: liqo-ca
  namespace: cert-manager
spec:
  isCA: true
  commonName: liqo-resource-broker-ca
  secretName: liqo-ca-secret
  duration: 87600h
  issuerRef:
    name: liqo-selfsigned-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: liqo-ca-issuer
spec:
  ca:
    secretName: liqo-ca-secret
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: broker-server-cert
  namespace: default
spec:
  secretName: broker-server-tls
  duration: 8760h
  renewBefore: 720h
  commonName: liqo-resource-broker
  subject:
    organizations:
      - LiqoResourceBroker
  dnsNames:
    - localhost
    - liqo-resource-broker
    - broker
    - broker-service
    - broker-cluster-control-plane
  ipAddresses:
    - 127.0.0.1
  usages:
    - server auth
    - client auth
  privateKey:
    algorithm: RSA
    size: 2048
  issuerRef:
    name: liqo-ca-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: agent-1-cert
  namespace: default
spec:
  secretName: agent-1-tls
  duration: 8760h
  renewBefore: 720h
  commonName: agent-cluster-1
  subject:
    organizations:
      - LiqoResourceAgent
  usages:
    - client auth
  privateKey:
    algorithm: RSA
    size: 2048
  issuerRef:
    name: liqo-ca-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: agent-2-cert
  namespace: default
spec:
  secretName: agent-2-tls
  duration: 8760h
  renewBefore: 720h
  commonName: agent-cluster-2
  subject:
    organizations:
      - LiqoResourceAgent
  usages:
    - client auth
  privateKey:
    algorithm: RSA
    size: 2048
  issuerRef:
    name: liqo-ca-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: agent-3-cert
  namespace: default
spec:
  secretName: agent-3-tls
  duration: 8760h
  renewBefore: 720h
  commonName: agent-cluster-3
  subject:
    organizations:
      - LiqoResourceAgent
  usages:
    - client auth
  privateKey:
    algorithm: RSA
    size: 2048
  issuerRef:
    name: liqo-ca-issuer
    kind: ClusterIssuer`

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifests)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to apply certificate manifests: %w", err)
	}

	fmt.Println("Waiting for certificates to be ready...")
	// Wait some time for the CA to be ready first
	time.Sleep(5 * time.Second)
	
	certs := []string{"liqo-ca", "broker-server-cert", "agent-1-cert", "agent-2-cert", "agent-3-cert"}
	for _, cert := range certs {
		ns := "default"
		if cert == "liqo-ca" {
			ns = "cert-manager"
		}
		cmd = exec.Command("kubectl", "wait", "--for=condition=Ready", "certificate/"+cert, "-n", ns, "--timeout=60s")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to wait for certificate %s: %w", cert, err)
		}
	}

	return nil
}

// TransferCertificates transfers certificates from the broker cluster to the agent clusters.
func TransferCertificates() error {
	fmt.Println("Transferring certificates to agent clusters...")

	// 1. Get CA from broker cluster
	if err := switchContext("kind-" + cluster.BrokerCluster); err != nil {
		return err
	}

	caSecret, err := getSecret("liqo-ca-secret", "cert-manager")
	if err != nil {
		return err
	}

	agent1Secret, err := getSecret("agent-1-tls", "default")
	if err != nil {
		return err
	}

	agent2Secret, err := getSecret("agent-2-tls", "default")
	if err != nil {
		return err
	}

	agent3Secret, err := getSecret("agent-3-tls", "default")
	if err != nil {
		return err
	}

	// 2. Apply secrets to agent clusters
	if err := applySecretToCluster(cluster.Agent1Cluster, "ca-secret", "default", caSecret); err != nil {
		return err
	}
	if err := applySecretToCluster(cluster.Agent1Cluster, "agent-tls", "default", agent1Secret); err != nil {
		return err
	}

	if err := applySecretToCluster(cluster.Agent2Cluster, "ca-secret", "default", caSecret); err != nil {
		return err
	}
	if err := applySecretToCluster(cluster.Agent2Cluster, "agent-tls", "default", agent2Secret); err != nil {
		return err
	}

	if err := applySecretToCluster(cluster.Agent3Cluster, "ca-secret", "default", caSecret); err != nil {
		return err
	}
	if err := applySecretToCluster(cluster.Agent3Cluster, "agent-tls", "default", agent3Secret); err != nil {
		return err
	}

	return nil
}

func switchContext(context string) error {
	cmd := exec.Command("kubectl", "config", "use-context", context)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to switch context to %s: %w", context, err)
	}
	return nil
}

func getSecret(name, namespace string) ([]byte, error) {
	cmd := exec.Command("kubectl", "get", "secret", name, "-n", namespace, "-o", "yaml")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}
	return output, nil
}
func applySecretToCluster(clusterName, newName, namespace string, secretContent []byte) error {
	fmt.Printf("Applying secret %s to cluster %s...\n", newName, clusterName)
	if err := switchContext("kind-" + clusterName); err != nil {
		return err
	}

	// Simple approach: modify the YAML in memory by replacing metadata fields.
	// We want to force the namespace to the target namespace and the name to newName.
	// We also need to strip out cluster-specific fields.
	
	lines := strings.Split(string(secretContent), "\n")
	var newLines []string
	skipFields := []string{"uid:", "resourceVersion:", "creationTimestamp:", "generation:", "selfLink:", "managedFields:"}
	
	for _, line := range lines {
		skip := false
		for _, field := range skipFields {
			if strings.Contains(line, field) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		
		// Replace name and namespace
		if strings.HasPrefix(strings.TrimSpace(line), "name:") {
			newLines = append(newLines, fmt.Sprintf("  name: %s", newName))
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "namespace:") {
			newLines = append(newLines, fmt.Sprintf("  namespace: %s", namespace))
			continue
		}
		
		newLines = append(newLines, line)
	}
	
	modifiedYAML := strings.Join(newLines, "\n")
	
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(modifiedYAML)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	return cmd.Run()
}
