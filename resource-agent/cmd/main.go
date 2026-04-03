/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"github.com/mehdiazizian/liqo-resource-agent/internal/publisher" // ← Add this
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport"
	transporthttp "github.com/mehdiazizian/liqo-resource-agent/internal/transport/http" // Used by NewCommunicator
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-agent/internal/controller"
	"github.com/mehdiazizian/liqo-resource-agent/internal/metrics"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(rearv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var brokerKubeconfig string // ← Add this line
	var brokerTransport string
	var brokerURL string
	var brokerCertPath string
	var clusterIDFlag string
	var advertisementName string
	var advertisementNamespace string
	var instructionNamespace string
	var brokerNamespace string
	var advertisementRequeueInterval time.Duration
	var instructionPollInterval time.Duration
	var kubeconfigsDir string
	var renewable bool
	var energyCost float64

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&brokerKubeconfig, "broker-kubeconfig", "", "Path to kubeconfig for broker cluster (optional)") // ← Add this line
	flag.StringVar(&brokerTransport, "broker-transport", "", "Transport protocol for broker communication (http|kubernetes, empty disables broker)")
	flag.StringVar(&brokerURL, "broker-url", "", "Broker URL for HTTP transport (e.g., https://broker.example.com:8443)")
	flag.StringVar(&brokerCertPath, "broker-cert-path", "", "Client certificate path for HTTP transport")
	flag.StringVar(&clusterIDFlag, "cluster-id", "", "Optional override for the agent cluster ID")
	flag.StringVar(&advertisementName, "advertisement-name", "cluster-advertisement", "Advertisement resource name")
	flag.StringVar(&advertisementNamespace, "advertisement-namespace", "default", "Advertisement namespace")
	flag.StringVar(&instructionNamespace, "instruction-namespace", "", "Namespace for ReservationInstruction objects (defaults to advertisement namespace)")
	flag.StringVar(&brokerNamespace, "broker-namespace", "default", "Namespace containing broker CRDs")
	flag.DurationVar(&advertisementRequeueInterval, "advertisement-requeue-interval", 30*time.Second, "Interval for periodic advertisement updates")
	flag.DurationVar(&instructionPollInterval, "instruction-poll-interval", 5*time.Second, "Interval for polling broker for provider instructions (0 to disable)")
	flag.StringVar(&kubeconfigsDir, "kubeconfigs-dir", "", "Directory containing kubeconfig files for Liqo peering (enables automatic peering)")
	flag.BoolVar(&renewable, "renewable", false, "Indicates if the cluster uses renewable energy")
	flag.Float64Var(&energyCost, "energy-cost", 0.0, "The cost of energy (0-1 normalization recommended)")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if instructionNamespace == "" {
		instructionNamespace = advertisementNamespace
	}

	ctx := context.Background()
	cfg := ctrl.GetConfigOrDie()

	bootstrapClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "failed to create bootstrap client")
		os.Exit(1)
	}

	clusterID := clusterIDFlag
	if clusterID == "" {
		ns := &corev1.Namespace{}
		if err := bootstrapClient.Get(ctx, client.ObjectKey{Name: "kube-system"}, ns); err != nil {
			setupLog.Error(err, "failed to read kube-system namespace for cluster-id")
			os.Exit(1)
		}
		clusterID = string(ns.UID)
	}
	setupLog.Info("Using cluster identifier", "clusterID", clusterID)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "e90c6511.fluidos.eu",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := ensureAdvertisementExists(ctx, bootstrapClient, advertisementNamespace, advertisementName, clusterID); err != nil {
		setupLog.Error(err, "unable to bootstrap advertisement resource",
			"namespace", advertisementNamespace, "name", advertisementName)
		os.Exit(1)
	}

	// =============================================================================
	// AGENT TRANSPORT SELECTION
	// =============================================================================
	// The agent uses ONE transport protocol to communicate with the broker,
	// selected by --broker-transport flag. Must match broker's --broker-interface.
	//
	// HINT: To enable MULTIPLE transports simultaneously (e.g., failover scenarios),
	// create multiple communicators and use them based on availability:
	//
	//   var communicators []transport.BrokerCommunicator
	//   if httpEnabled {
	//       httpComm, _ := NewCommunicator("http", ...)
	//       communicators = append(communicators, httpComm)
	//   }
	//   if mqttEnabled {
	//       mqttComm, _ := NewCommunicator("mqtt", ...)
	//       communicators = append(communicators, mqttComm)
	//   }
	//   // Use first available, or implement failover logic
	//
	// =============================================================================

	var brokerClient *publisher.BrokerClient
	var brokerCommunicator transport.BrokerCommunicator

	// Support legacy kubeconfig flag (maps to kubernetes transport)
	if brokerKubeconfig != "" && brokerTransport == "" {
		brokerTransport = "kubernetes"
	}

	if brokerTransport != "" {
		setupLog.Info("Initializing broker communicator",
			"transport", brokerTransport,
			"clusterID", clusterID)

		switch brokerTransport {
		case "http":
			var err error
			brokerCommunicator, err = NewCommunicator(
				brokerTransport,
				brokerURL,
				brokerKubeconfig,
				brokerCertPath,
				clusterID,
			)
			if err != nil {
				setupLog.Error(err, "failed to create HTTP broker communicator")
				os.Exit(1)
			}
			setupLog.Info("HTTP broker communicator initialized successfully",
				"brokerURL", brokerURL)

		case "kubernetes":
			// Legacy Kubernetes transport using BrokerClient
			var err error
			brokerClient, err = publisher.NewBrokerClient(brokerKubeconfig, clusterID, brokerNamespace)
			if err != nil {
				setupLog.Error(err, "failed to create broker client")
				os.Exit(1)
			}
			setupLog.Info("Kubernetes broker client initialized successfully")

		default:
			setupLog.Error(nil, "Unknown broker transport", "transport", brokerTransport)
			os.Exit(1)
		}
	} else {
		setupLog.Info("Broker transport not specified, broker communication disabled")
	}

	if err = (&controller.AdvertisementReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		MetricsCollector: &metrics.Collector{
			ClusterIDOverride: clusterID,
		},
		BrokerClient:         brokerClient,         // Legacy Kubernetes transport
		BrokerCommunicator:   brokerCommunicator,   // New transport abstraction (HTTP)
		RequeueInterval:      advertisementRequeueInterval,
		InstructionNamespace: instructionNamespace,  // For provider instructions from response
		TargetKey: types.NamespacedName{
			Name:      advertisementName,
			Namespace: advertisementNamespace,
		},
		Renewable:  renewable,
		EnergyCost: energyCost,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Advertisement")
		os.Exit(1)
	}

	if err = (&controller.ProviderInstructionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ProviderInstruction")
		os.Exit(1)
	}

	if err = (&controller.ReservationInstructionReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		KubeconfigsDir: kubeconfigsDir,
		ClusterID:      clusterID,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ReservationInstruction")
		os.Exit(1)
	}

	// ResourceRequest controller: handles synchronous reservation requests to the broker
	if err = (&controller.ResourceRequestReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		BrokerCommunicator:   brokerCommunicator,
		InstructionNamespace: instructionNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ResourceRequest")
		os.Exit(1)
	}

	// Start instruction poller for near-instant provider instruction delivery (HTTP transport)
	if brokerCommunicator != nil && instructionPollInterval > 0 {
		poller := &controller.InstructionPoller{
			Client:               mgr.GetClient(),
			BrokerCommunicator:   brokerCommunicator,
			PollInterval:         instructionPollInterval,
			InstructionNamespace: instructionNamespace,
		}
		go func() {
			if err := poller.Start(context.Background()); err != nil {
				setupLog.Error(err, "Instruction poller failed")
			}
		}()
		setupLog.Info("Instruction poller started", "interval", instructionPollInterval)
	}

	// Start Reservation Watcher if broker client is available (Kubernetes transport)
	if brokerClient != nil && brokerClient.Enabled {
		watcher := publisher.NewReservationWatcher(brokerClient, mgr.GetClient(), instructionNamespace)
		go func() {
			if err := watcher.Start(context.Background()); err != nil {
				setupLog.Error(err, "Reservation watcher failed")
			}
		}()
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func ensureAdvertisementExists(
	ctx context.Context,
	k8sClient client.Client,
	namespace, name, clusterID string,
) error {
	adv := &rearv1alpha1.Advertisement{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, adv); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		zeroMetrics := defaultResourceMetrics()
		adv = &rearv1alpha1.Advertisement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: rearv1alpha1.AdvertisementSpec{
				ClusterID: clusterID,
				Resources: zeroMetrics,
				Timestamp: metav1.Now(),
			},
		}
		return k8sClient.Create(ctx, adv)
	}

	return nil
}

func defaultResourceMetrics() rearv1alpha1.ResourceMetrics {
	zeroCPU := resource.NewQuantity(0, resource.DecimalSI)
	zeroMem := resource.NewQuantity(0, resource.BinarySI)

	zeroQuantities := rearv1alpha1.ResourceQuantities{
		CPU:    *zeroCPU,
		Memory: *zeroMem,
	}

	return rearv1alpha1.ResourceMetrics{
		Capacity:    zeroQuantities,
		Allocatable: zeroQuantities,
		Allocated:   zeroQuantities,
		Available:   zeroQuantities,
	}
}

// NewCommunicator creates a BrokerCommunicator based on transport type
func NewCommunicator(
	transportType string,
	brokerURL string,
	brokerKubeconfig string,
	certPath string,
	clusterID string,
) (transport.BrokerCommunicator, error) {
	switch transportType {
	case "http":
		if brokerURL == "" {
			return nil, fmt.Errorf("broker-url is required for HTTP transport")
		}
		if certPath == "" {
			return nil, fmt.Errorf("broker-cert-path is required for HTTP transport")
		}
		return transporthttp.NewHTTPCommunicator(brokerURL, certPath, clusterID)

	case "kubernetes":
		return nil, fmt.Errorf("kubernetes transport not yet implemented (coming in future iteration)")

	default:
		return nil, fmt.Errorf("unknown transport type: %s (supported: http, kubernetes)", transportType)
	}
}
