package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/netgroup-polito/k8s-resource-brokering/tests/internal/cluster"
)

// BuildAndLoadImages builds the agent and broker images and loads them into Kind clusters.
func BuildAndLoadImages(rootDir string) error {

	//GO: Viene definita al volo un'array di struct anonime con name e path e subito dopo (nelle graffe) viene creata una sua istanza
	components := []struct {
		name string
		path string
	}{
		{"resource-broker", filepath.Join(rootDir, "resource-broker")},
		{"resource-agent", filepath.Join(rootDir, "resource-agent")},
	}
	for _, comp := range components {
		if comp.name == "resource-agent" {
			if err := downloadBinaries(comp.path); err != nil {
				return fmt.Errorf("failed to download binaries for %s: %w", comp.name, err)
			}
		}

		fmt.Printf("Building image for %s...\n", comp.name)
		cmd := exec.Command("docker", "build", "-t", comp.name+":latest", ".")
		cmd.Dir = comp.path
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to build image %s: %w", comp.name, err)
		}

		fmt.Printf("Loading image %s into clusters...\n", comp.name)
		clusters := []string{cluster.BrokerCluster, cluster.Agent1Cluster, cluster.Agent2Cluster}
		for _, cls := range clusters {
			cmd = exec.Command("kind", "load", "docker-image", comp.name+":latest", "--name", cls)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to load image %s into cluster %s: %w", comp.name, cls, err)
			}
		}
	}

	return nil
}

// DeployBroker deploys the broker to the broker cluster.
func DeployBroker(rootDir string) error {
	fmt.Println("Deploying broker...")
	if err := switchContext("kind-" + cluster.BrokerCluster); err != nil {
		return err
	}
	// Apply CRDs
	crdPath := filepath.Join(rootDir, "resource-broker", "config", "crd", "bases")
	cmd := exec.Command("kubectl", "apply", "-f", crdPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to apply broker CRDs: %w", err)
	}

	//builds the path to the broker manifest file.
	manifestPath := filepath.Join(rootDir, "resource-broker", "deploy", "manifests.yaml")
	
	//GO: viene creato un comando "kubectl apply -f <manifestPath>" che applica il manifest del broker al cluster. L'output e l'errore del comando vengono indirizzati a stdout e stderr.
	cmd = exec.Command("kubectl", "apply", "-f", manifestPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func switchContext(context string) error {
	cmd := exec.Command("kubectl", "config", "use-context", context)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to switch context to %s: %w", context, err)
	}
	return nil
}

// DeployAgents deploys the agents to their respective clusters.
func DeployAgents(rootDir string, kubeconfigsDir string) error {
	
	//GO: vedi linea 16
	agents := []struct {
		clusterName string
		id          string
		renewable   string
		cost        string
	}{
		{cluster.Agent1Cluster, "agent-cluster-1", "true", "0.5"},
		{cluster.Agent2Cluster, "agent-cluster-2", "false", "0.8"},
	}

	for _, agent := range agents {
		fmt.Printf("Deploying agent to %s...\n", agent.clusterName)
		if err := switchContext("kind-" + agent.clusterName); err != nil {
			return err
		}
		// Apply CRDs
		crdPath := filepath.Join(rootDir, "resource-agent", "config", "crd", "bases")
		cmd := exec.Command("kubectl", "apply", "-f", crdPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to apply agent CRDs to %s: %w", agent.clusterName, err)
		}

		// 1. Create the kubeconfigs secret
		if err := createKubeconfigsSecret(kubeconfigsDir, agent.clusterName); err != nil {
			return err
		}

		// 2. Apply manifests with environment overrides
		// Since we want PLAIN YAML, we'll use a hack with 'sed' or just template it in memory.
		// For now, let's use a temporary modified file.
		manifestPath := filepath.Join(rootDir, "resource-agent", "deploy", "manifests.yaml")
		content, err := os.ReadFile(manifestPath)
		if err != nil {
			return err
		}

		modifiedContent := string(content)
		modifiedContent = strings.ReplaceAll(modifiedContent, "value: \"agent-cluster-1\" # To be overridden or templated", "value: \""+agent.id+"\"")
		modifiedContent = strings.ReplaceAll(modifiedContent, "value: \"true\"", "value: \""+agent.renewable+"\"")
		modifiedContent = strings.ReplaceAll(modifiedContent, "value: \"0.5\"", "value: \""+agent.cost+"\"")

		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(modifiedContent)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to deploy agent to %s: %w", agent.clusterName, err)
		}
	}

	return nil
}

func createKubeconfigsSecret(kubeconfigsDir string, clusterName string) error {
	fmt.Println("Creating kubeconfigs secret for", clusterName, "...")

	args := []string{"create", "secret", "generic", "agent-kubeconfigs", "--namespace", "default", "--save-config", "--dry-run=client", "-o", "yaml"}
	
	kubeconfigFile := filepath.Join(kubeconfigsDir, clusterName+"-internal.kubeconfig")
	if _, err := os.Stat(kubeconfigFile); os.IsNotExist(err) {
		return fmt.Errorf("kubeconfig not found for %s: %s", clusterName, kubeconfigFile)
	}

	args = append(args, "--from-file="+clusterName+".kubeconfig="+kubeconfigFile)

	// Apply the secret
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	
	createCmd := exec.Command("kubectl", args...)
	output, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create secret dry-run: %w", err)
	}
	
	cmd.Stdin = strings.NewReader(string(output)) //GO: il comando "kubectl create secret generic agent-kubeconfigs --namespace default --save-config --dry-run=client -o yaml --from-file=..." viene eseguito e il suo output (che è il manifest YAML del secret) viene passato come input al comando "kubectl apply -f -" per creare o aggiornare il secret nel cluster.
	cmd.Stdout = os.Stdout //GO: l'output del comando viene indirizzato a stdout
	cmd.Stderr = os.Stderr //GO: l'errore del comando viene indirizzato a stderr
	return cmd.Run()
}

func downloadBinaries(agentDir string) error {
	// 1. Download kubectl
	kubectlPath := filepath.Join(agentDir, "kubectl")
	if _, err := os.Stat(kubectlPath); os.IsNotExist(err) {
		fmt.Println("  -> Downloading kubectl...")
		versionCmd := exec.Command("curl", "-L", "-s", "https://dl.k8s.io/release/stable.txt")
		version, err := versionCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get kubectl version: %w", err)
		}
		v := strings.TrimSpace(string(version))
		downloadUrl := fmt.Sprintf("https://dl.k8s.io/release/%s/bin/linux/amd64/kubectl", v)
		
		cmd := exec.Command("curl", "-Lo", kubectlPath, downloadUrl)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to download kubectl: %w", err)
		}
		os.Chmod(kubectlPath, 0755)
	}

	// 2. Download liqoctl
	liqoctlPath := filepath.Join(agentDir, "liqoctl")
	if _, err := os.Stat(liqoctlPath); os.IsNotExist(err) {
		fmt.Println("  -> Downloading liqoctl...")
		
		// Use direct GitHub download as get.liqo.io is failing to resolve
		tarUrl := "https://github.com/liqotech/liqo/releases/latest/download/liqoctl-linux-amd64.tar.gz"
		tarPath := filepath.Join(agentDir, "liqoctl.tar.gz")
		
		// Download tarball
		dlCmd := exec.Command("curl", "-Lo", tarPath, tarUrl)
		if err := dlCmd.Run(); err != nil {
			return fmt.Errorf("failed to download liqoctl tarball: %w", err)
		}
		
		// Extract tarball
		extractCmd := exec.Command("tar", "-xzf", tarPath, "-C", agentDir)
		if err := extractCmd.Run(); err != nil {
			return fmt.Errorf("failed to extract liqoctl: %w", err)
		}
		
		// Cleanup tarball
		os.Remove(tarPath)
		os.Chmod(liqoctlPath, 0755)
	}
	return nil
}
