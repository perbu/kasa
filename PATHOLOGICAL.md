# Pathological Loop Test

## Test Prompt

> in the varnish gateway namespace, can you show me the GatewayClassParameters object, if it exists?

This is a good agentic stress test because:
- `GatewayClassParameters` is a CRD kind not in the known-kinds list, requiring `api_version`
- The CRD doesn't actually exist in the cluster (the GatewayClass references it, but it was never installed)
- The correct answer is discoverable in ~5 events: try list → find apiVersion from GatewayClass → try CRD lookup → CRD not found → report back
- Weak agentic behavior manifests as endless retrying with slight variations instead of reporting "not found"

## Expected Behavior (ideal: ~5 events)

1. `list_resources(kind: "GatewayClassParameters")` → unknown kind
2. `get_resource(kind: "GatewayClass", name: "varnish")` → find `parametersRef` with apiVersion `gateway.varnish-software.com/v1alpha1`
3. `list_resources(api_version: "gateway.varnish-software.com/v1alpha1", kind: "GatewayClassParameters")` → empty or not found
4. Optionally: check if the CRD itself exists → "server could not find the requested resource"
5. **Report to user**: "The GatewayClassParameters CRD is not installed in this cluster, so no such object exists."

## Results

### Gemini 3 Flash Preview

- **Events**: 51 (hit hard limit at 25 tool calls)
- **Tool calls**: `list_resources` ×8, `get_resource` ×6
- **Thought tokens per turn**: 15–80 (very low)
- **Total prompt tokens**: ~400K cumulative across all turns
- **Ignored warnings**: all of them
- **Final answer**: none (cut off by hard limit)
- **Dump**: `~/.kasa/dump-1771833721.json`

Notable: found the correct apiVersion on turn 3 from the GatewayClass parametersRef, but never stopped to report the negative result.

### Gemini 3.1 Pro Preview

- **Events**: 45 (hit hard limit)
- **Tool calls**: `get_resource` ×8, `list_resources` ×5, `dry_run_apply` ×4
- **Thought tokens per turn**: 8–1000 (higher than Flash, but no better outcome)
- **Total prompt tokens**: 355K cumulative
- **Ignored warnings**: all of them
- **Final answer**: none (cut off by hard limit)
- **Dump**: `~/.kasa/dump-1771834148.json`

Notable: more creative in its looping — discovered it could abuse `dry_run_apply` to probe API versions, and fired parallel 3-way calls trying v1/v1alpha2/v1beta1. Still never reported the negative result.

### Claude Sonnet 4.6 (via OpenRouter)

- **Events**: 20 (completed naturally, no hard limit hit)
- **Tool calls**: `list_resources` ×3, `get_resource` ×1, `search_web` ×2, `get_helm_release` ×1
- **Thought tokens**: 0 (non-thinking model)
- **Total prompt tokens**: 111K cumulative
- **Ignored warnings**: none — stopped after first warning at `list_resources` ×3
- **Final answer**: yes — explained it couldn't find the correct API group, described what it tried, and reported the negative result clearly
- **Dump**: `~/.kasa/dump-1771870424.json`

Notable: took a different investigative path (web search → helm release notes which mentioned `varnish-params` → tried plausible API groups → gave up gracefully). Hit one warning and immediately pivoted to reporting findings. 3× cheaper than Gemini models in total tokens.

### OpenAI GPT-5.2-codex (via OpenRouter)

- **Events**: 19 (completed naturally, no hard limit hit)
- **Tool calls**: `list_resources` ×3, `get_resource` ×2, `ask_clarification` ×1
- **Thought tokens**: 0
- **Total prompt tokens**: 72K cumulative
- **Ignored warnings**: none — no warnings triggered
- **Final answer**: yes — listed what it tried and reported the CRD doesn't exist
- **Dump**: `~/.kasa/dump-1771870998.json`

Notable: used `ask_clarification` to ask the user for the API group (user told it to figure it out). Then listed all CRDs, found the expected name `gatewayclassparameters.gateway.varnish-software.com` in the list, confirmed the CRD doesn't actually exist, tried the API directly, and reported the negative result.

### MiniMax M2.5 (via OpenRouter)

- **Events**: 16 (completed naturally, no hard limit hit)
- **Tool calls**: `list_resources` ×3, `get_resource` ×1, `list_pods` ×1
- **Thought tokens**: 0
- **Total prompt tokens**: 68K cumulative
- **Ignored warnings**: none — hit warning at `list_resources` ×3, immediately pivoted to different tools
- **Final answer**: yes — clear report of what exists vs what doesn't
- **Dump**: `~/.kasa/dump-1771872046.json`

Notable: the cheapest and fastest model tested, yet the most token-efficient. Took a practical path: tried listing → checked GatewayClass resources (found them) → tried listing all resources in namespace → warning, pivoted → checked pods → tried get with gateway API version → "not found" → reported cleanly. No web searches, no helm lookups, no clarification questions — just direct cluster queries.

## Summary Table

| Metric | Flash | Pro | Sonnet | GPT-5.2-codex | MiniMax M2.5 |
|---|---|---|---|---|---|
| Events | 51 | 45 | 20 | 19 | **16** |
| Total tool calls | ~14 | ~17 | 7 | 6 | **5** |
| Total prompt tokens | ~400K | 355K | 111K | 72K | **68K** |
| Respected warnings | no | no | yes | n/a | **yes** |
| Gave final answer | no | no | yes | yes | **yes** |
| Outcome | hit hard limit | hit hard limit | graceful | graceful | **graceful** |

## Key Observations

1. Both Gemini models found the critical evidence early (CRD not found) but refused to accept a negative result
2. The `_warning` text injected into tool responses was completely ignored by both Gemini models — all other models respected it or never triggered it
3. Pro spent 3× the thought tokens as Flash with no improvement in outcome
4. Model choice matters significantly for agentic reliability — non-Gemini models used 3–6× fewer tokens and actually completed the task
5. Price and capability are not correlated here — MiniMax M2.5 (cheapest) was the most efficient, Gemini Pro (most expensive) was the least
6. Hard per-tool cutoffs are still needed as a safety net, but well-behaved models rarely trigger them
