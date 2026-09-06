# Kasa - kubernetes agentic system administration

Kasa is a conversational Kubernetes deployment assistant. It uses Google's ADK (Agent Development Kit) with Gemini and
client-go for Kubernetes interaction.

## Features

- Interactive REPL with safe mode, mutating operations require approval.
- Manifest management with git history tracking
- Support for core Kubernetes resources and CRDs, Gateway API and others.
- **Drift detection** — background scanning compares stored manifests
  against the live cluster. Results are cached for 24 hours and
  automatically invalidated after any mutating operation. Use `/drift`
  for a detailed report or ask the agent (it calls the `show_drift` tool). 

## Build

```bash
go build -o kasa .
```

## Configuration

Run `kasa init` to create a config file at `~/.kasa/config.yaml`:

```bash
./kasa init
```

Edit `~/.kasa/config.yaml` to add your API keys and customize settings. You need a
Google API key for Gemini. A Jina key is optional (for web search and URL fetching).

Environment variables (`GOOGLE_API_KEY`, `JINA_API_KEY`)
override config file values.

## Usage

```bash
./kasa                           # Interactive mode
./kasa init                      # Create config file
./kasa -prompt "list namespaces" # Single prompt mode
./kasa -debug -prompt "..."      # Debug output
```

When the manifests repository has an `origin` remote (including one configured
with `deployments.remote`), Kasa automatically pulls and rebases at startup,
before reading manifests or checking drift. This runs in both interactive and
single-prompt mode. Pull failures are reported without stopping Kasa; resolve
the Git issue and use `/pull` to retry. Repositories without a remote are skipped.

## Safe Mode

In interactive mode, mutating operations require approval. The agent proposes a
plan, you review it, then approve with yes` or reject with `no`.

## License

Apache License 2.0. See [LICENSE](LICENSE).
