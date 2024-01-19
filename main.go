package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type ingressResult struct {
	name           string
	namespace      string
	hostname       string
	backendService string
}

func doesIngressExist(serviceName, namespace string, results map[string][]ingressResult) bool {
	for _, i := range results[namespace] {
		if i.backendService == serviceName {
			return true
		}
	}
	return false
}

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	ingresses, err := clientset.NetworkingV1().Ingresses("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	fmt.Printf("Found %d ingress resources\n", len(ingresses.Items))

	results := make(map[string][]ingressResult)

	for _, i := range ingresses.Items {
		if i.Spec.DefaultBackend != nil {
			fmt.Printf("Default backend defined: %#v\n", i.Spec.DefaultBackend)

			if !doesIngressExist(i.Spec.DefaultBackend.Service.Name, i.Namespace, results) {
				r := ingressResult{
					name:           i.Name,
					namespace:      i.Namespace,
					hostname:       "",
					backendService: i.Spec.DefaultBackend.Service.Name,
				}
				results[i.Namespace] = append(results[i.Namespace], r)
			}
		}

		for _, h := range i.Spec.Rules {
			for _, p := range h.HTTP.Paths {
				if !doesIngressExist(p.Backend.Service.Name, i.Namespace, results) {

					r := ingressResult{
						name:           i.Name,
						namespace:      i.Namespace,
						hostname:       h.Host,
						backendService: p.Backend.Service.Name,
					}
					results[i.Namespace] = append(results[i.Namespace], r)
				}
			}
		}
	}

	totalResults := 0
	for _, v := range results {
		totalResults += len(v)
	}
	fmt.Printf("%d results (after filtering)\n", totalResults)

	for n, s := range results {
		for _, i := range s {
			service, err := clientset.CoreV1().Services(n).Get(context.TODO(), i.backendService, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				fmt.Printf("Not found service: %s\n", i.backendService)
				continue
			} else if err != nil {
				panic(err)
			}

			if service.Spec.Type != "ExternalName" {
				//fmt.Printf("Processing service %s (%s)\n", i.backendService, i.namespace)

				labelSelector := metav1.LabelSelector{MatchLabels: service.Spec.Selector}
				listOptions := metav1.ListOptions{
					LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
				}
				pods, err := clientset.CoreV1().Pods(i.namespace).List(context.TODO(), listOptions)
				if err != nil {
					panic(err.Error())
				}

				if len(pods.Items) <= 0 {
					fmt.Printf("No active pods found for ingress %s, skipping\n", i.name)
					continue
				}
				//fmt.Printf("Found %d pods. First pod: %s\n", len(pods.Items), pods.Items[0].Name)

				pod := pods.Items[0]
				if pod.Spec.SecurityContext.RunAsNonRoot == nil || *pod.Spec.SecurityContext.RunAsNonRoot != true {
					fmt.Printf("RunAsNonRoot is not set to true for service %s (pod: %s)\n", i.backendService, pod.Name)
				}
				for _, container := range pod.Spec.Containers {
					if container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation != false {
						fmt.Printf("AllowPrivilegeEscalation is set to false for service %s (pod: %s, container: %s)\n", i.backendService, pod.Name, container.Name)
					}
					if container.SecurityContext.ReadOnlyRootFilesystem == nil || *container.SecurityContext.ReadOnlyRootFilesystem != true {
						fmt.Printf("ReadOnlyRootFilesystem is not enabled for service %s (pod: %s, container: %s)\n", i.backendService, pod.Name, container.Name)
					}
				}

				fmt.Println()
			}
		}
	}
}
