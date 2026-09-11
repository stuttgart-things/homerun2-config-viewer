// Package kube builds the Kubernetes client the viewer reads with.
package kube

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientset returns a clientset for kubeconfigPath, or for the in-cluster
// service account when the path is empty.
func NewClientset(kubeconfigPath string) (kubernetes.Interface, error) {
	cfg, err := restConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	// The viewer only lists and gets; identify it in API server audit logs.
	cfg.UserAgent = "homerun2-config-viewer"

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return cs, nil
}

func restConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath == "" {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config (set KUBECONFIG to run outside a cluster): %w", err)
		}
		return cfg, nil
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return cfg, nil
}
