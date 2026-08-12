# Repository Guidelines

## Project Structure & Module Organization

This repository is a small Go 1.25 command-line client for an LLM chat API. `main.go` contains the interactive CLI and output formatting. `config/api.go` owns HTTP requests, conversation history, SSE parsing, and the callback, channel, and iterator streaming APIs; keep additional client code in this package. `test/` currently contains learning/example types, not automated tests. Architectural and streaming notes live in `docs/`. Treat `ai-test.exe` and files under `log/` as generated artifacts; do not edit or commit new binaries or runtime logs.

## Build, Test, and Development Commands

- `go run .` starts the interactive client; enter `exit` to stop it. A valid API key/configuration is required for live requests.
- `go build ./...` compiles every package and catches package-level build failures.
- `go test ./...` runs all Go tests. It currently reports packages with no test files, so add tests with behavioral changes.
- `gofmt -w .` formats all Go sources. Use `gofmt -l .` to check formatting without modifying files.
- `go vet ./...` performs standard static analysis before review.

## Coding Style & Naming Conventions

Follow idiomatic Go and let `gofmt` control tabs and layout. Package names should be short and lowercase; exported identifiers use `PascalCase`, unexported identifiers use `camelCase`, and constructors follow `NewType` (for example, `NewClient`). Keep receiver names short and consistent (`llm *LLM`). Return errors explicitly and close HTTP response bodies with `defer`. Preserve the three streaming interfaces unless a change deliberately updates their documented contracts.

## Testing Guidelines

Use Go's standard `testing` package. Place tests beside implementation files and name them `*_test.go`; name test functions `TestBehavior`. Prefer table-driven tests and `httptest.Server` for request/response behavior. Cover SSE `[DONE]`, malformed JSON, empty or `null` deltas, HTTP failures, early iterator termination, and history trimming. Tests must not call the live API.

## Commit & Pull Request Guidelines

Git history is unavailable in this checkout, so use concise imperative subjects such as `Handle malformed SSE chunks`. Keep commits focused. Pull requests should explain the behavior change, list verification commands, and note API or streaming-contract changes. Link relevant issues; include terminal output only when CLI presentation changes.

## Security & Configuration

Never commit API keys, tokens, prompts containing private data, or captured model responses. Prefer environment variables for credentials and endpoints. Review generated logs before sharing them, and keep secrets out of tests and documentation examples.
