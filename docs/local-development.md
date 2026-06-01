# Local Development

Run unit tests:

```bash
go test ./...
```

Run the FolioSpace fixture integration test:

```bash
CODEX_GATEWAY_RUN_INTEGRATION=1 go test ./tests/integration -v
```

Start the gateway with the sample config:

```bash
go run ./cmd/codex-issue-gateway --config configs/example.yml
```
