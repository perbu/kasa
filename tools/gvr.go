package tools

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// CommonGVRs maps known resource kinds (lowercase) to their GroupVersionResource.
// This covers core Kubernetes resources and common CRDs.
var CommonGVRs = map[string]schema.GroupVersionResource{
	// Core resources
	"pod":                   {Group: "", Version: "v1", Resource: "pods"},
	"service":              {Group: "", Version: "v1", Resource: "services"},
	"configmap":            {Group: "", Version: "v1", Resource: "configmaps"},
	"secret":               {Group: "", Version: "v1", Resource: "secrets"},
	"namespace":            {Group: "", Version: "v1", Resource: "namespaces"},
	"persistentvolumeclaim": {Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
	"serviceaccount":       {Group: "", Version: "v1", Resource: "serviceaccounts"},

	// Apps resources
	"deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"statefulset": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"daemonset":   {Group: "apps", Version: "v1", Resource: "daemonsets"},
	"replicaset":  {Group: "apps", Version: "v1", Resource: "replicasets"},

	// Batch resources
	"job":     {Group: "batch", Version: "v1", Resource: "jobs"},
	"cronjob": {Group: "batch", Version: "v1", Resource: "cronjobs"},

	// Networking resources
	"ingress":       {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	"networkpolicy": {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},

	// RBAC resources
	"role":               {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	"rolebinding":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
	"clusterrole":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
	"clusterrolebinding": {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},

	// Gateway API resources
	"gateway":       {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"},
	"httproute":     {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
	"grpcroute":     {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"},
	"tcproute":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tcproutes"},
	"udproute":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "udproutes"},
	"tlsroute":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tlsroutes"},
	"referencegrant": {Group: "gateway.networking.k8s.io", Version: "v1beta1", Resource: "referencegrants"},
	"gatewayclass":  {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"},

	// cert-manager resources
	"certificate":   {Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
	"issuer":        {Group: "cert-manager.io", Version: "v1", Resource: "issuers"},
	"clusterissuer": {Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"},
	"certificaterequest": {Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests"},

	// Autoscaling
	"horizontalpodautoscaler": {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
}

// KindAliases maps common aliases to their canonical kind names.
var KindAliases = map[string]string{
	"po":      "pod",
	"pods":    "pod",
	"svc":     "service",
	"services": "service",
	"cm":      "configmap",
	"configmaps": "configmap",
	"secrets": "secret",
	"ns":      "namespace",
	"namespaces": "namespace",
	"pvc":     "persistentvolumeclaim",
	"persistentvolumeclaims": "persistentvolumeclaim",
	"sa":      "serviceaccount",
	"serviceaccounts": "serviceaccount",
	"deploy":      "deployment",
	"deployments": "deployment",
	"sts":         "statefulset",
	"statefulsets": "statefulset",
	"ds":          "daemonset",
	"daemonsets":  "daemonset",
	"rs":          "replicaset",
	"replicasets": "replicaset",
	"jobs":        "job",
	"cronjobs":    "cronjob",
	"ing":         "ingress",
	"ingresses":   "ingress",
	"netpol":      "networkpolicy",
	"networkpolicies": "networkpolicy",
	"roles":       "role",
	"rolebindings": "rolebinding",
	"clusterroles": "clusterrole",
	"clusterrolebindings": "clusterrolebinding",
	"gw":          "gateway",
	"gateways":    "gateway",
	"httproutes":  "httproute",
	"grpcroutes":  "grpcroute",
	"tcproutes":   "tcproute",
	"udproutes":   "udproute",
	"tlsroutes":   "tlsroute",
	"referencegrants": "referencegrant",
	"gatewayclasses": "gatewayclass",
	"gc":          "gatewayclass",
	"cert":        "certificate",
	"certificates": "certificate",
	"issuers":     "issuer",
	"clusterissuers": "clusterissuer",
	"certificaterequests": "certificaterequest",
	"cr":          "certificaterequest",
	"hpa":         "horizontalpodautoscaler",
	"horizontalpodautoscalers": "horizontalpodautoscaler",
}

// ClusterScopedKinds lists kinds that are cluster-scoped (not namespaced).
var ClusterScopedKinds = map[string]bool{
	"namespace":            true,
	"clusterrole":          true,
	"clusterrolebinding":   true,
	"clusterissuer":        true,
	"gatewayclass":         true,
}

// NormalizeKindName converts a kind string (possibly an alias) to its canonical lowercase form.
func NormalizeKindName(kind string) string {
	k := strings.ToLower(kind)
	if canonical, ok := KindAliases[k]; ok {
		return canonical
	}
	return k
}

// LookupGVR looks up the GroupVersionResource for a kind name.
// Returns the GVR and true if found, or zero value and false if not found.
func LookupGVR(kind string) (schema.GroupVersionResource, bool) {
	normalized := NormalizeKindName(kind)
	gvr, ok := CommonGVRs[normalized]
	return gvr, ok
}

// IsNamespaced returns true if the kind is namespaced (not cluster-scoped).
func IsNamespaced(kind string) bool {
	normalized := NormalizeKindName(kind)
	return !ClusterScopedKinds[normalized]
}

// ParseYAMLToUnstructured parses YAML content into an unstructured.Unstructured object.
func ParseYAMLToUnstructured(content []byte) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(content, &obj.Object); err != nil {
		return nil, err
	}
	return obj, nil
}

// GVKToGVR converts a GroupVersionKind to a GroupVersionResource.
// It uses simple pluralization rules (adds 's' or 'es').
func GVKToGVR(gvk schema.GroupVersionKind) schema.GroupVersionResource {
	// First check if we have a known GVR for this kind
	if gvr, ok := LookupGVR(gvk.Kind); ok {
		// Use the version from the GVK but the resource name from our lookup
		return schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: gvr.Resource,
		}
	}

	// Fall back to simple pluralization
	resource := strings.ToLower(gvk.Kind)
	if strings.HasSuffix(resource, "s") || strings.HasSuffix(resource, "x") ||
		strings.HasSuffix(resource, "ch") || strings.HasSuffix(resource, "sh") {
		resource += "es"
	} else {
		resource += "s"
	}

	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resource,
	}
}

// ParseAPIVersion parses an apiVersion string into group and version.
// For example: "apps/v1" -> ("apps", "v1"), "v1" -> ("", "v1")
func ParseAPIVersion(apiVersion string) (group, version string) {
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// BuildGVRFromKindAndAPIVersion builds a GVR from a kind and apiVersion string.
// If apiVersion is empty, it tries to look up the GVR from CommonGVRs.
func BuildGVRFromKindAndAPIVersion(kind, apiVersion string) (schema.GroupVersionResource, bool) {
	normalized := NormalizeKindName(kind)

	// If no apiVersion provided, try to look up from known resources
	if apiVersion == "" {
		return LookupGVR(normalized)
	}

	// Parse the apiVersion
	group, version := ParseAPIVersion(apiVersion)

	// Try to get the resource name from known GVRs
	if gvr, ok := CommonGVRs[normalized]; ok {
		return schema.GroupVersionResource{
			Group:    group,
			Version:  version,
			Resource: gvr.Resource,
		}, true
	}

	// Fall back to simple pluralization
	resource := normalized
	if strings.HasSuffix(resource, "s") || strings.HasSuffix(resource, "x") ||
		strings.HasSuffix(resource, "ch") || strings.HasSuffix(resource, "sh") {
		resource += "es"
	} else {
		resource += "s"
	}

	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}, true
}
