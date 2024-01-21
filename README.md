# README

Script for querying all K8s services in the current context which have an ingress route - either via an ingress rule or load balancer service - 
but do not have certain security contexts enabled:

1. RunAsNonRoot in the pod security context
2. AllowPrivilegeEscalation in the container security context
3. ReadOnlyRootFilesystem in the container security context

Used as part of a security hardening exercise of internet facing services.

Currently, outputs the offending services to the console only.

## Run

```shell
# Point the kubeconfig to the relevant context
kubectl config use-context <context>

# Run app
go run main.go
```