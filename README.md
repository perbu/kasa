# Kasa - kubernetes agentic system administration

Kasa is a conversational Kubernetes deployment assistant. It uses Google's ADK (Agent Development Kit) with Gemini and
client-go for Kubernetes interaction.

## Features

- Interactive REPL with safe mode, mutating operations require approval.
- Manifest management with git history tracking
- Support for core Kubernetes resources and CRDs, Gateway API and others. 

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
Google API key for Gemini. Jina and Tavily keys are optional (for web fetch and search).

Environment variables (`GOOGLE_API_KEY`, `JINA_READER_API_KEY`, `TAVILY_API_KEY`)
override config file values.

## Usage

```bash
./kasa                           # Interactive mode
./kasa init                      # Create config file
./kasa -prompt "list namespaces" # Single prompt mode
./kasa -debug -prompt "..."      # Debug output
```

## Safe Mode

In interactive mode, mutating operations require approval. The agent proposes a
plan, you review it, then approve with yes` or reject with `no`.

## License

Apache License 2.0. See [LICENSE](LICENSE).
