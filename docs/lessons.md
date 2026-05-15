# Lessons from Building Kasa

Kasa is a conversational Kubernetes deployment agent. These are the lessons learned from building it — most of them the hard way.

## LLMs Will Hallucinate User Approval

The most dramatic finding: the model would generate fake `/approve` responses after calling `propose_plan`, then proceed to execute mutating tools without actual user consent. System prompt instructions ("wait for user approval") were not enough.

The fix was to cancel the agent context the moment a plan is detected, making it structurally impossible for the model to continue past the approval gate. **System prompts alone are not a safety mechanism.** If the consequence of non-compliance is dangerous, enforce it in code.

## The Plan/Approval Pattern with Parameter Pinning

Kasa's plan workflow goes beyond simple confirmation dialogs. When a user approves a plan, each action has its targeting parameters (namespace, name, kind) pinned. The LLM can't deviate from what was approved — wrong parameters means the mutation guard blocks the call. Each action executes exactly once, then becomes unavailable.

Body parameters (like YAML content) are intentionally not pinned. Only *what is targeted* matters for safety. This distinction keeps the system flexible while preventing the model from applying changes to the wrong resources.

## Multi-Layered Defense Against Loops

Agent loops are the most common failure mode. Kasa evolved four independent layers through painful experience:

1. **System prompt guidance** — A "3-strike rule": if a resource isn't found after 3 attempts with different API versions/namespaces/formats, stop and report to the user.
2. **Per-tool call warnings** — After 3 invocations of the same tool in one turn, a `_warning` field is injected into the response: "You have called list_resources 3 times this turn. Are you stuck in a loop?"
3. **Hard global limit** — A configurable ceiling (default 25) on total tool calls per turn. Exceeding it cancels the agent context.
4. **Connection error streak detection** — 3 consecutive connection errors across any tools sets a sticky flag and injects a critical warning to stop calling tools entirely.

No single layer is sufficient. Some models ignore warnings entirely (see the benchmark below). The hard limit exists because every softer mechanism can fail.

## Secrets Never Touch the LLM

Three-pronged approach to keeping credentials out of the model's context:

- **DirectIO side channel**: The `show_secret` and `create_secret` tools communicate directly with the user through a parallel output buffer. The LLM only sees metadata like `"status": "displayed_to_user", "key_names": ["password", "api_key"]`. Secret values never appear in the conversation.
- **Redaction at collection**: When `get_resource` retrieves a Secret, the data and stringData fields are stripped before the result is returned to the model.
- **Exclusion from drift analysis**: Secrets are excluded from drift comparisons entirely. The agent can't access Secret data from the cluster, so comparing stored manifests against live state would produce misleading results anyway.

## Forced Reasoning via "reason" Parameter

Every tool declaration gets an auto-injected `reason` parameter. The model must articulate *why* it's calling each tool. This reason is displayed to the user in real-time.

It's a cheap transparency mechanism. Users can follow the agent's logic without reading raw logs, and it makes loop debugging much easier — you can see the model repeating the same justification.

## The PATHOLOGICAL Benchmark

A single prompt that stress-tests agent reliability:

> "In the varnish-gateway namespace, can you show me the GatewayClassParameters object, if it exists?"

This is pathological because GatewayClassParameters is a custom CRD not in the known-kinds list, and it doesn't actually exist in the cluster. The correct behavior is roughly 5 events: list resources, check the GatewayClass for an API version hint, try listing with that version, find nothing, report back.

Empirical results:

| Model | Events | Cumulative Tokens | Respected Warnings |
|-------|--------|-------------------|-------------------|
| Gemini Flash | 51 | 400K | No |
| Gemini Pro | 45 | 355K | No |
| Claude Sonnet | 20 | 111K | Yes |
| GPT-5.2 | 19 | 72K | N/A |
| MiniMax M2.5 | 16 | 68K | Yes |

Gemini models made up to 24 calls for the same missing CRD and completely ignored injected warnings. Price and capability are not correlated. This benchmark is maintained in the codebase to catch regressions and evaluate new models.

## Tool Wrapping for Cross-Cutting Concerns

Every tool is wrapped in a `countingTool` decorator that transparently handles loop detection, mutation guarding, and connection error tracking. Individual tool implementations stay clean and focused on their domain logic. Safety is architectural, not per-tool.

This is the decorator pattern applied to LLM tooling. It means adding a new tool doesn't require remembering to add safety checks — they come for free.

## Embedded Reference Documentation

Kubernetes resource documentation is compiled into the binary via Go's `//go:embed` directive. The LLM can look up how Deployments, Services, Gateway API resources, and cert-manager objects work without making network calls. The knowledge is consistent, fast, and always available.

This matters because LLMs have imprecise knowledge of Kubernetes resource schemas, especially for CRDs. Giving the agent authoritative reference material at zero latency reduces hallucination.

## The Gemini to OpenRouter Pivot

Kasa was originally built on Google's Gemini SDK. Empirical testing (the PATHOLOGICAL benchmark) showed 3-6x token efficiency differences between models and revealed that Gemini models didn't respect safety warning injections.

The response was to build a vendored OpenAI-compatible adapter, so any provider works — OpenRouter, local models, or direct API access. The architecture decision was driven entirely by benchmarking, not theory or brand preference.

## The Overarching Theme

Building safe agentic systems requires defense in depth. No single layer — prompts, warnings, limits, approval gates — is sufficient alone. Each layer in Kasa was added after a specific failure mode was discovered empirically. The system is the sum of its scars.
