# Kasa

Kasa is a conversational Kubernetes deployment assistant. It uses Google's ADK (Agent Development Kit) with Gemini and client-go for Kubernetes interaction.

## Features

- Interactive REPL with safe mode (mutating operations require approval)
- Manifest management with git integration
- Support for core Kubernetes resources and CRDs (Gateway API, cert-manager)
- Dynamic client fallback for unknown resource types

## Build

```bash
go build -o kasa .
```

## Configuration

Create a `.env` file with your API key:

```
GOOGLE_API_KEY=your-key-here
```

Edit `config.yaml` for Kubernetes settings and model selection.

## Usage

```bash
./kasa                           # Interactive mode
./kasa -prompt "list namespaces" # Single prompt mode
./kasa -debug -prompt "..."      # Debug output
```

## Safe Mode

In interactive mode, mutating operations require approval. The agent proposes a plan, you review it, then approve with `yes` or reject with `no`.

## License

Apache License 2.0. See [LICENSE](LICENSE).
