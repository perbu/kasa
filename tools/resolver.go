package tools

import (
	"log"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// discoveredResource holds the resolved GVR and scope for a discovered API resource.
type discoveredResource struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
}

// ResourceResolver uses the Kubernetes discovery API to resolve resource kinds
// that aren't in the hardcoded CommonGVRs map. Results are cached after the
// first lookup.
type ResourceResolver struct {
	discovery discovery.DiscoveryInterface
	cache     map[string]discoveredResource // lowercase kind → info
	mu        sync.RWMutex
	loaded    bool
}

// NewResourceResolver creates a new ResourceResolver backed by the given discovery client.
func NewResourceResolver(dc discovery.DiscoveryInterface) *ResourceResolver {
	return &ResourceResolver{
		discovery: dc,
	}
}

// Resolve looks up a resource kind via the discovery API cache.
// Returns the GVR, whether the resource is namespaced, and whether it was found.
func (r *ResourceResolver) Resolve(kind string) (schema.GroupVersionResource, bool, bool) {
	if r == nil {
		return schema.GroupVersionResource{}, false, false
	}

	k := strings.ToLower(kind)

	r.mu.RLock()
	if r.loaded {
		res, ok := r.cache[k]
		r.mu.RUnlock()
		return res.GVR, res.Namespaced, ok
	}
	r.mu.RUnlock()

	// Cache not loaded yet — load it.
	r.loadCache()

	r.mu.RLock()
	defer r.mu.RUnlock()
	res, ok := r.cache[k]
	return res.GVR, res.Namespaced, ok
}

// ResolveGVK resolves a GroupVersionKind to a GVR using the discovery cache.
// It matches on lowercase kind and, if group is non-empty, also on group.
func (r *ResourceResolver) ResolveGVK(gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool) {
	if r == nil {
		return schema.GroupVersionResource{}, false
	}

	k := strings.ToLower(gvk.Kind)

	r.mu.RLock()
	if !r.loaded {
		r.mu.RUnlock()
		r.loadCache()
		r.mu.RLock()
	}
	defer r.mu.RUnlock()

	res, ok := r.cache[k]
	if !ok {
		return schema.GroupVersionResource{}, false
	}

	// If caller specified a group, verify it matches.
	if gvk.Group != "" && res.GVR.Group != gvk.Group {
		// Group mismatch — scan all entries (rare path for ambiguous kinds).
		for _, entry := range r.cache {
			entryKind := strings.TrimSuffix(entry.GVR.Resource, "s") // rough check
			if entry.GVR.Group == gvk.Group && (entryKind == k || entry.GVR.Resource == k+"s" || entry.GVR.Resource == k+"es") {
				return schema.GroupVersionResource{
					Group:    gvk.Group,
					Version:  gvk.Version,
					Resource: entry.GVR.Resource,
				}, true
			}
		}
		// Fall back to the resource name we found, with the caller's group/version.
		return schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: res.GVR.Resource,
		}, true
	}

	// Use the discovered resource name but the caller's group/version.
	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: res.GVR.Resource,
	}, true
}

// loadCache fetches all API resources from the discovery endpoint and populates the cache.
func (r *ResourceResolver) loadCache() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.loaded {
		return // another goroutine loaded while we waited
	}

	cache := make(map[string]discoveredResource)

	// ServerPreferredResources returns preferred versions for all groups.
	// It may return partial results alongside an error for unavailable groups.
	lists, err := r.discovery.ServerPreferredResources()
	if err != nil {
		// Partial results are still usable.
		if lists == nil {
			log.Printf("discovery: failed to fetch API resources: %v", err)
			return // don't set loaded — allow retry
		}
		log.Printf("discovery: partial error fetching API resources (using partial results): %v", err)
	}

	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}

		for _, res := range list.APIResources {
			// Skip subresources (e.g., pods/log, deployments/scale).
			if strings.Contains(res.Name, "/") {
				continue
			}

			k := strings.ToLower(res.Kind)

			// Don't overwrite — first match wins (preferred version).
			if _, exists := cache[k]; exists {
				continue
			}

			cache[k] = discoveredResource{
				GVR: schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: res.Name,
				},
				Namespaced: res.Namespaced,
			}
		}
	}

	r.cache = cache
	r.loaded = true
}
