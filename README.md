# README

App for querying all K8s services in the current context which have an ingress rule but do not have certain security contexts enabled:

1. RunAsNonRoot
2. AllowPrivilegeEscalation
3. ReadOnlyRootFilesystem

Used as part of a hardening exercise of internet facing services.