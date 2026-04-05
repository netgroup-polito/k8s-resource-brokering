package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/netgroup-polito/k8s-resource-brokering/tests/internal/certmanager"
	"github.com/netgroup-polito/k8s-resource-brokering/tests/internal/cluster"
	"github.com/netgroup-polito/k8s-resource-brokering/tests/internal/deploy"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "setup":
		if err := runSetup(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	case "deploy":
		if err := runDeploy(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	case "cleanup":
		if err := runCleanup(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: setup <command>")
	fmt.Println("Commands:")
	fmt.Println("  setup   - Create Kind clusters and install Liqo")
	fmt.Println("  deploy  - Build images and deploy Agent and Broker to clusters")
	fmt.Println("  cleanup - Delete Kind clusters")
}

func runSetup() error {
	fmt.Println("==============================================")
	fmt.Println("  Setting up test environment")
	fmt.Println("==============================================")

	if err := cluster.CreateClusters(); err != nil {
		return err
	}

	workdir, _ := os.Getwd()
	// Adjust path based on execution location
	kubeconfigsDir := filepath.Join(workdir, "test-setup", "kubeconfigs")
	if _, err := os.Stat(kubeconfigsDir); os.IsNotExist(err) {
		kubeconfigsDir = filepath.Join(workdir, "..", "test-setup", "kubeconfigs")
	}

	if err := cluster.ExportKubeconfigs(kubeconfigsDir); err != nil {
		return err
	}

	if err := cluster.InstallLiqo(kubeconfigsDir); err != nil {
		return err
	}

	fmt.Println("==============================================")
	fmt.Println("  Setup complete!")
	fmt.Println("==============================================")
	return nil
}

func runDeploy() error {
	fmt.Println("==============================================")
	fmt.Println("  Deploying components to clusters")
	fmt.Println("==============================================")

	workdir, _ := os.Getwd()
	rootDir := workdir
	if _, err := os.Stat(filepath.Join(rootDir, "resource-agent")); os.IsNotExist(err) {
		rootDir = filepath.Join(workdir, "..")
	}

	// 1. Build and Load Images
	if err := deploy.BuildAndLoadImages(rootDir); err != nil {
		return err
	}

	// 2. Setup Certificates
	if err := certmanager.SetupCertManager(); err != nil {
		return err
	}
	if err := certmanager.TransferCertificates(); err != nil {
		return err
	}

	// 3. Deploy Broker
	if err := deploy.DeployBroker(rootDir); err != nil {
		return err
	}

	// 4. Deploy Agents
	kubeconfigsDir := filepath.Join(rootDir, "tests", "test-setup", "kubeconfigs")
	if err := deploy.DeployAgents(rootDir, kubeconfigsDir); err != nil {
		return err
	}

	fmt.Println("==============================================")
	fmt.Println("  Deployment complete!")
	fmt.Println("==============================================")
	return nil
}

func runCleanup() error {
	fmt.Println("==============================================")
	fmt.Println("  Cleaning up test environment")
	fmt.Println("==============================================")

	if err := cluster.Cleanup(); err != nil {
		return err
	}

	fmt.Println("==============================================")
	fmt.Println("  Cleanup complete!")
	fmt.Println("==============================================")
	return nil
}

