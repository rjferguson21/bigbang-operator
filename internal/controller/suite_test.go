/*
Copyright 2026 Big Bang.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/internal/controller"
)

// testEnv is the shared envtest harness for the reconciler tests in this
// package. startEnv boots envtest, installs the operator's CRD plus the
// istio CRDs the reconciler watches, and launches the manager. Cleanup is
// registered with the testing.T so callers don't have to teardown manually.
type testEnv struct {
	cfg    *envtest.Environment
	k8s    client.Client
	scheme *runtime.Scheme
}

func startEnv(t *testing.T) *testEnv {
	t.Helper()
	logf.SetLogger(zap.New(zap.WriteTo(testWriter{t}), zap.UseDevMode(true)))

	te := &testEnv{
		cfg: &envtest.Environment{
			CRDDirectoryPaths: []string{
				filepath.Join("..", "..", "config", "crd", "bases"),
				filepath.Join("testdata", "crds"),
			},
			ErrorIfCRDPathMissing: true,
		},
		scheme: runtime.NewScheme(),
	}
	utilruntime.Must(corev1.AddToScheme(te.scheme))
	utilruntime.Must(networkingv1.AddToScheme(te.scheme))
	utilruntime.Must(istiosecv1.AddToScheme(te.scheme))
	utilruntime.Must(istionetv1.AddToScheme(te.scheme))
	utilruntime.Must(bbv1alpha1.AddToScheme(te.scheme))

	restCfg, err := te.cfg.Start()
	if err != nil {
		t.Fatalf("envtest start: %v", err)
	}
	t.Cleanup(func() { _ = te.cfg.Stop() })

	te.k8s, err = client.New(restCfg, client.Options{Scheme: te.scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	skipNameValidation := true
	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 te.scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		// Each test spins up its own manager in the same process; without
		// this the second manager errors because the controller name
		// `package` is already registered globally for metrics.
		Controller: config.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if err := (&controller.PackageReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := mgr.Start(ctx); err != nil {
			t.Logf("manager exited: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	return te
}

func (te *testEnv) ensureNamespace(t *testing.T, name string) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := te.k8s.Create(context.Background(), ns); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create ns %s: %v", name, err)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(string(b))
	return len(b), nil
}

// waitFor polls fn until it returns nil or 10s pass. Use for asserting
// reconciler-observed state, which lands asynchronously.
func waitFor(t *testing.T, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return
		} else {
			last = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("wait failed: %v", last)
}
