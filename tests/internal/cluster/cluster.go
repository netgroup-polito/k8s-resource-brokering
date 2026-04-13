package cluster

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	BrokerCluster = "broker-cluster"
	Agent1Cluster = "agent-cluster-1"
	Agent2Cluster = "agent-cluster-2"
)

// CreateClusters creates the required kind clusters if they don't exist.
/* It creates 3 clusters using the kind tool:
- broker-cluster: the cluster where the broker will be deployed
- agent-cluster-1/2: the cluster where the agents will be deployed
*/
func CreateClusters() error {
	//GO: con []string viene definito un array di stringhe (i nomi dei cluster)
	clusters := []string{BrokerCluster, Agent1Cluster, Agent2Cluster}

	//GO: creiamo due variabili che prendono come valori il risultato della funzione getKindClusters() e un eventuale errore
	existingClusters, err := getKindClusters()
	if err != nil {
		return err
	}

	//GO: "range" itera su ogni elemento di clusters (il singolo elemento sta in name) senza iteratore esplicito (perchéc'è "_")
	for _, name := range clusters {

		//GO: in questo caso l'if viene fatto sul risultato della funzione contains (che restiuisce un bool)
		if contains(existingClusters, name) {
			fmt.Printf("Cluster %s already exists, skipping...\n", name)
			continue
		}

		fmt.Printf("Creating cluster %s...\n", name)

		//GO: viene creata una variabile cmd che è un comando da eseguire, in questo caso "kind create cluster --name <name>" (con una configurazione specifica per il broker-cluster)
		var cmd *exec.Cmd

		//broker-cluster needs a specific configuration to map the API server port to the host, so we define a custom config for it. Agent clusters can be created with default configuration.
		if name == BrokerCluster {
			// Broker cluster needs specific port mapping
			config := `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30443
    hostPort: 8443
    protocol: TCP`
			cmd = exec.Command("kind", "create", "cluster", "--name", name, "--config", "-")

			//GO: cmd.Stdin è un reader che legge la stringa config e la passa al comando come input standard
			cmd.Stdin = strings.NewReader(config)
		} else {
			// agent clusters can be created with default configuration
			cmd = exec.Command("kind", "create", "cluster", "--name", name)
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create cluster %s: %w", name, err)
		}
	}

	return nil
}

// getKindClusters returns a list of existing kind cluster names, running the command "kind get clusters" and parsing the output.
//"kind get clusters" returns a list of cluster names, one per line. We split the output by newlines and return the resulting slice of strings.
func getKindClusters() ([]string, error) {
	cmd := exec.Command("kind", "get", "clusters")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get kind clusters: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return lines, nil
}


func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// ExportKubeconfigs exports kubeconfigs for the agent clusters to the specified directory.
func ExportKubeconfigs(destDir string) error {
	//MkdirAll tries to create the specified directory with 0755 permission (r/w/e for owner, r/e for group and others)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create kubeconfigs directory: %w", err)
	}

	//Agent1Cluster and Agent2Cluster are two constants defined at lines 12-13
	clusters := []string{Agent1Cluster, Agent2Cluster}
	for _, name := range clusters {
		// 1. Export External Kubeconfig (for host tools like liqoctl install)
		fmt.Printf("Exporting kubeconfig for %s...\n", name)
		cmd := exec.Command("kind", "get", "kubeconfig", "--name", name)
		output, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get external kubeconfig for %s: %w", name, err)
		}
		filePath := fmt.Sprintf("%s/%s.kubeconfig", destDir, name)
		if err := os.WriteFile(filePath, output, 0644); err != nil {
			return fmt.Errorf("failed to write external kubeconfig for %s: %w", name, err)
		}

		// 2. Export Internal Kubeconfig (for pod-to-pod communication)
		fmt.Printf("Exporting internal kubeconfig for %s...\n", name)
		internalCmd := exec.Command("kind", "get", "kubeconfig", "--name", name, "--internal")
		internalOutput, err := internalCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get internal kubeconfig for %s: %w", name, err)
		}
		
		internalFilePath := fmt.Sprintf("%s/%s-internal.kubeconfig", destDir, name)
		if err := os.WriteFile(internalFilePath, internalOutput, 0644); err != nil {
			return fmt.Errorf("failed to write internal kubeconfig for %s: %w", name, err)
		}
	}

	return nil
}

// PatchCoreDNS updates the CoreDNS configmap in all clusters to resolve other cluster's control-plane hostnames to their Docker IPs.
func PatchCoreDNS() error {
	clusters := []string{BrokerCluster, Agent1Cluster, Agent2Cluster}
	hostsBlock := "        hosts {\n"
	
	for _, name := range clusters {
		containerName := name + "-control-plane"
		ipCmd := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerName)
		ipOutput, err := ipCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get docker IP for %s: %w", containerName, err)
		}
		dockerIP := strings.TrimSpace(string(ipOutput))
		hostsBlock += fmt.Sprintf("           %s %s\n", dockerIP, containerName)
	}
	hostsBlock += "           fallthrough\n        }\n"

	for _, name := range clusters {
		fmt.Printf("Patching CoreDNS in %s...\n", name)
		
		// Get current Corefile
		getCmd := exec.Command("kubectl", "--context", "kind-"+name, "get", "configmap", "coredns", "-n", "kube-system", "-o", "jsonpath={.data.Corefile}")
		corefileBytes, err := getCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get Corefile for %s: %w", name, err)
		}
		
		corefile := string(corefileBytes)
		if !strings.Contains(corefile, "hosts {") {
			// Inject hosts block before the 'forward' directive or 'cache'
			corefile = strings.Replace(corefile, "forward .", hostsBlock+"        forward .", 1)
			
			// Apply update
			patchCtx := fmt.Sprintf(`{"data": {"Corefile": %q}}`, corefile)
			patchCmd := exec.Command("kubectl", "--context", "kind-"+name, "patch", "configmap", "coredns", "-n", "kube-system", "--type", "merge", "-p", patchCtx)
			if err := patchCmd.Run(); err != nil {
				return fmt.Errorf("failed to patch CoreDNS for %s: %w", name, err)
			}
			
			// Restart CoreDNS pods to apply changes
			restartCmd := exec.Command("kubectl", "--context", "kind-"+name, "rollout", "restart", "deployment", "coredns", "-n", "kube-system")
			restartCmd.Run()
		}
	}

	return nil
}

// InstallLiqo installs Liqo on the agent clusters.
func InstallLiqo(kubeconfigsDir string) error {
	if _, err := exec.LookPath("liqoctl"); err != nil {
		fmt.Println("WARNING: liqoctl is not installed! Skipping Liqo installation...")
		return nil
	}

	clusters := []string{Agent1Cluster, Agent2Cluster}
	for _, name := range clusters {
		fmt.Printf("Installing Liqo on %s...\n", name)
		kubeconfigPath := fmt.Sprintf("%s/%s.kubeconfig", kubeconfigsDir, name)

		// Using 'liqoctl install kind' with --cluster-id and --skip-confirm for automation
		cmd := exec.Command("liqoctl", "install", "kind", "--kubeconfig", kubeconfigPath, "--cluster-id", name, "--skip-confirm")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install Liqo on %s: %w", name, err)
		}
	}

	return nil
}

// Cleanup deletes all test kind clusters.
func Cleanup() error {
	clusters := []string{BrokerCluster, Agent1Cluster, Agent2Cluster}
	for _, name := range clusters {
		fmt.Printf("Deleting cluster %s...\n", name)
		_ = exec.Command("kind", "delete", "cluster", "--name", name).Run()
	}
	return nil
}
