# Tools Package TODO

From AI code review (reviewer tool, 2026-02-06).

## High Priority

- [ ] **SSRF in http_request.go** — No filtering of private IPs, localhost, or cloud metadata endpoints (169.254.169.254). Add an allowlist or block RFC 1918 ranges and known metadata IPs.
- [ ] **Context propagation** — Tools create `context.WithTimeout(context.Background(), ...)` instead of using the parent `ctx`. Cancellation doesn't propagate. Use `context.WithTimeout(ctx, ...)` throughout.
- [ ] **Unbounded log reading in logs.go** — `io.ReadAll(stream)` with no byte cap. Use `io.LimitReader` to enforce a hard limit (e.g. 1MB).
- [ ] **Multi-doc YAML in apply_resource.go / gvr.go** — `ParseYAMLToUnstructured` only handles the first document in a `---`-separated stream. Use a YAML decoder loop.

## Medium Priority

- [ ] **Plaintext secrets in git** — `secret_create.go` and `import.go` save Secret data as plaintext YAML via `SaveManifest`. Consider SealedSecrets/ExternalSecrets integration, or skip saving secrets to disk.
- [ ] **Brittle pluralization in gvr.go** — `GVKToGVR` falls back to heuristic pluralization (`+ "es"` / `+ "s"`). Use the discovery client to resolve GVRs accurately.
- [ ] **Consolidate apply paths** — `ApplyManifestTool` (typed) and `ApplyResourceTool` (dynamic) duplicate logic. Consider unifying on the dynamic client approach.

## Low Priority

- [ ] **Jina reader privacy in fetch.go** — URLs are proxied through `r.jina.ai`. Users should be aware of the third-party data exposure.
- [ ] **Repetitive argument parsing** — Every `Run` method does manual `argsMap["key"].(string)`. Consider a helper for typed argument extraction.
- [ ] **Recursive diff depth in drift.go** — `DiffMaps` uses unbounded recursion. Add a depth limiter for safety.
