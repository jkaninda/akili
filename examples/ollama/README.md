# Akili + Ollama (Fully Local)

Run Akili with Ollama for a fully local deployment — no cloud API keys required.

## Stack

| Service | Description |
|---------|-------------|
| **akili** | Akili gateway (HTTP API on `:8080`) |
| **ollama** | Local LLM inference server |
| **ollama-pull** | Init container that pulls the model on first run |
| **postgres** | PostgreSQL 16 for persistent storage |

## Quick Start

```bash
# Start the stack
docker compose up -d

# First run: wait for the model to download (watch progress)
docker compose logs -f ollama-pull

# Verify Akili is healthy
curl http://localhost:8080/healthz
```

## Usage

### Send a query

```bash
curl -X POST http://localhost:8080/v1/query \
  -H "Authorization: Bearer dev-key" \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello, what can you do?"}'
```

### Stream a response (SSE)

```bash
curl -N -X POST http://localhost:8080/v1/query/stream \
  -H "Authorization: Bearer dev-key" \
  -H "Content-Type: application/json" \
  -d '{"message": "Explain what a cron job is"}'
```

### Swagger UI

Open [http://localhost:8080/docs](http://localhost:8080/docs) in your browser.

## Change the Model

Edit `config.json` and update the `providers.ollama.model` field:

```json
{
  "providers": {
    "default": "ollama",
    "ollama": {
      "model": "llama3.1",
      "base_url": "http://ollama:11434"
    }
  }
}
```

Then update the model name in `docker-compose.yml` (the `ollama-pull` service entrypoint) and restart:

```bash
docker compose down
docker compose up -d
```

## GPU Support

To enable GPU acceleration for Ollama, uncomment the `deploy` section in `docker-compose.yml`:

```yaml
ollama:
  deploy:
    resources:
      reservations:
        devices:
          - driver: nvidia
            count: all
            capabilities: [gpu]
```

## Cleanup

```bash
# Stop and remove containers
docker compose down

# Stop and remove containers + volumes (deletes all data)
docker compose down -v
```
