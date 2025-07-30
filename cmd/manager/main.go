package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	v1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"github.com/GoogleCloudPlatform/karo/pkg/controller"
	"github.com/GoogleCloudPlatform/karo/pkg/transformer"
	"k8s.io/apimachinery/pkg/runtime"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	k8szap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	//+kubebuilder:scaffold:imports
)

var (
	// The scheme that this operator will handle.
	scheme = runtime.NewScheme()
	// setupLog represents the logger that we use during the setup phase of the
	// manager.
	setupLog = ctrl.Log.WithName("setup")
	// The user agent string we will use in conjunction with REST requests.
	userAgent = "ai-connector/0.1.0"
)

func main() {

	// Register schemas
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))

	if err := run(ctrl.SetupSignalHandler()); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var leaderElectionNamespace string
	var enableHTTP2 bool
	var logEncoder string
	var watchNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "", "Namespace where the leader election resource lives. Defaults to the pod namespace if not set.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&logEncoder, "log-encoder", "console", "Encoder to use for logging. Valid values are 'json' and 'console'. Defaults to 'json'")
	flag.StringVar(&watchNamespace, "watch-namespace", "", "Specify a list of namespaces to watch for custom resources, separated by commas. If left empty, all namespaces will be watched.")

	logOptions := k8szap.Options{
		Development: true,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}

	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	// Configure and set the logger.
	stdoutEncoder, err := newLogEncoder(logEncoder)
	if err != nil {
		setupLog.Error(err, "failed to create log encoder")
		return fmt.Errorf("failed to create log encoder: %v", err)
	}
	logOptions.Encoder = stdoutEncoder
	ctrl.SetLogger(k8szap.New(k8szap.UseFlagOptions(&logOptions)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancelation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}
	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	options := ctrl.Options{
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{},
		},
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
			TLSOpts:     tlsOpts,
		},
		WebhookServer:           webhookServer,
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "ai-connector-for-gke",
		LeaderElectionNamespace: leaderElectionNamespace,
	}

	// Set up the manager cache.
	watchNamespaces := strings.Split(watchNamespace, ",")
	if len(watchNamespaces) == 1 && watchNamespaces[0] == "" {
		setupLog.Info("Flag watch-namespace is not set. Watch custom resources in all namespaces.")
	} else {
		setupLog.Info("Only watch custom resources in specific namespaces.", "namespaces", watchNamespaces)
		for _, namespace := range watchNamespaces {
			options.Cache.DefaultNamespaces[namespace] = cache.Config{}
		}
	}

	setupLog.Info("Setup manager")
	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = userAgent
	mgr, err := ctrl.NewManager(restConfig, options)
	if err != nil {
		setupLog.Error(err, "Unable to create manager")
		return fmt.Errorf("unable to create manager: %v", err)
	}

	// Register the integration controller, it will register everything else.
	reconciler := &controller.IntegrationReconciler{
		Client:      mgr.GetClient(),
		Manager:     mgr,
		Transformer: transformer.NewTransformer(),
		Scheme:      mgr.GetScheme(),
		KindReconcilers: map[string]controller.KindReconciler{
			"ModelData":      &controller.ModelDataReconciler{},
			"AgenticSandbox": &controller.AgenticSandboxReconciler{},
		},
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Integration")
		os.Exit(1)
	}
	setupLog.Info("Registered controller", "controller", "Integration")

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to add health check")
		return fmt.Errorf("unable to add health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to add ready check")
		return fmt.Errorf("unable to add ready check: %v", err)
	}
	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Problem running manager")
		return fmt.Errorf("problem running manager: %v", err)
	}
	return nil
}

// newLogEncoder returns a zapcore.Encoder based on the encoder type ('json' or 'console')
func newLogEncoder(encoderType string) (zapcore.Encoder, error) {
	pe := zap.NewProductionEncoderConfig()
	pe.EncodeTime = zapcore.ISO8601TimeEncoder
	if encoderType == "json" || encoderType == "" {
		return zapcore.NewJSONEncoder(pe), nil
	}
	if encoderType == "console" {
		return zapcore.NewConsoleEncoder(pe), nil
	}
	return nil, fmt.Errorf("invalid encoder %q (must be 'json' or 'console')", encoderType)
}
