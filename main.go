package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// result stores information about a single service which provides an ingress (ingress or load balancer) into the k8s environment.
type result struct {
	name             string            // Ingress name for ingress based routes, service name for load balancer based routes
	namespace        string            // Which namespace does the service belong in
	backendService   string            // The backend k8s service which we are routing to
	serviceSelectors map[string]string // The pod selectors used for the backend service
}

// alreadyInResultsSlice checks if the namespaced service has already been stored in the results map.
// This helps to dedup the services, so we are only checking each once.
func alreadyInResultsSlice(serviceName, namespace string, results map[string][]result) bool {
	for _, i := range results[namespace] {
		if i.backendService == serviceName {
			return true
		}
	}
	return false
}

// processService queries for the k8s service and returns a result struct for further processing.
// The 2nd return value is whether this resource should be skipped.
func processService(clientset *kubernetes.Clientset, namespace, ingressName, backendServiceName string) (result, bool, error) {
	var r result
	service, err := clientset.CoreV1().Services(namespace).Get(context.TODO(), backendServiceName, metav1.GetOptions{})

	if k8sErrors.IsNotFound(err) {
		return r, true, nil
	}
	// Does not contain any pods
	if service.Spec.Type == "ExternalName" {
		return r, true, nil
	}
	// Handled separately from the ingress rules
	if service.Spec.Type == "LoadBalancer" {
		return r, true, nil
	}
	if err != nil {
		return r, false, fmt.Errorf("error whilst listing service: %w", err)
	}

	r = result{
		name:             ingressName,
		namespace:        namespace,
		backendService:   backendServiceName,
		serviceSelectors: service.Spec.Selector,
	}

	return r, false, nil
}

// checkSecurityContexts checks whether the services listed in the results map have certain k8s security contexts enabled.
// Currently just outputs to the console.
func checkSecurityContexts(clientset *kubernetes.Clientset, results map[string][]result) error {
	for namespace, slice := range results {
		for _, i := range slice {
			labelSelector := metav1.LabelSelector{MatchLabels: i.serviceSelectors}
			listOptions := metav1.ListOptions{
				LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
			}
			pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), listOptions)
			if err != nil {
				return fmt.Errorf("error whilst listing pods: %w", err)
			}

			if len(pods.Items) <= 0 {
				fmt.Printf("No active pods found for ingress %s (service %s, namespace: %s), skipping\n", i.name, i.backendService, i.namespace)
				continue
			}

			// Check just the first pod
			pod := pods.Items[0]
			if pod.Spec.SecurityContext.RunAsNonRoot == nil || *pod.Spec.SecurityContext.RunAsNonRoot != true {
				fmt.Printf("%s: RunAsNonRoot is not set to true (pod: %s)\n", i.backendService, pod.Name)
			}
			for _, container := range pod.Spec.Containers {
				if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation != false {
					fmt.Printf("%s: AllowPrivilegeEscalation is not set to false for service (pod: %s, container: %s)\n", i.backendService, pod.Name, container.Name)
				}
				if container.SecurityContext == nil || container.SecurityContext.ReadOnlyRootFilesystem == nil || *container.SecurityContext.ReadOnlyRootFilesystem != true {
					fmt.Printf("%s: ReadOnlyRootFilesystem is not enabled for service (pod: %s, container: %s)\n", i.backendService, pod.Name, container.Name)
				}
			}
			fmt.Println()
		}
	}

	return nil
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

	// stores the deduplicated services as a slice, keyed by namespace
	results := make(map[string][]result)

	// Check for services which have at least 1 ingress route
	for _, i := range ingresses.Items {

		// Using a default backend
		if i.Spec.DefaultBackend != nil {
			fmt.Printf("Default backend defined: %#v\n", i.Spec.DefaultBackend)

			if !alreadyInResultsSlice(i.Spec.DefaultBackend.Service.Name, i.Namespace, results) {
				r, skip, err := processService(clientset, i.Namespace, i.Name, i.Spec.DefaultBackend.Service.Name)
				if skip {
					continue
				}
				if err != nil {
					panic(err.Error())
				}
				results[i.Namespace] = append(results[i.Namespace], r)
			}
		}

		// Using HTTP host paths
		for _, h := range i.Spec.Rules {
			for _, p := range h.HTTP.Paths {

				if !alreadyInResultsSlice(p.Backend.Service.Name, i.Namespace, results) {
					r, skip, err := processService(clientset, i.Namespace, i.Name, p.Backend.Service.Name)
					if skip {
						continue
					}
					if err != nil {
						panic(err.Error())
					}
					results[i.Namespace] = append(results[i.Namespace], r)
				}
			}
		}
	}

	// Check for services which have a LoadBalancer ingress
	loadBalancerServices, err := clientset.CoreV1().Services("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	for _, svc := range loadBalancerServices.Items {
		if svc.Spec.Type == "LoadBalancer" {
			r := result{
				name:             svc.Name,
				namespace:        svc.Namespace,
				backendService:   svc.Name,
				serviceSelectors: svc.Spec.Selector,
			}
			results[svc.Namespace] = append(results[svc.Namespace], r)
		}
	}

	totalResults := 0
	for _, v := range results {
		totalResults += len(v)
	}
	fmt.Printf("%d results (after filtering)\n\n", totalResults)

	// Validate security contexts
	err = checkSecurityContexts(clientset, results)
	if err != nil {
		panic(err.Error())
	}
}
