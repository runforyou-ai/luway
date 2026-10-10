# Luway

[中文](README.zh-CN.md)

Luway is an open-source, self-hosted AI-native collaboration application built with Go, Wails v3, and React. One codebase supports the server, web application, desktop clients, and mobile clients.

## Local development

Use the Go version and exact Wails CLI version specified in `go.mod`, Node.js 24 or later, Docker, and a PostgreSQL client providing `psql`. Run commands from the repository root.

```bash
cp .env.example .env
wails3 task db:up
wails3 task db:ensure
wails3 task migrate
wails3 task run:server
```

Open `http://localhost:8080/app/`. The development database listens on `127.0.0.1:5432`. Compose uses project `luway`, container `luway-postgres-1`, and volume `luway_postgres-data`. The default database is `main`; server tests recreate `main_test` on each run.

The root Taskfile loads `.env` and includes shared tasks from `build/tasks.yml`. Platform tasks live in `build/<platform>/Taskfile.yml`. Additional worktrees need distinct database names and server, Vite, and MCP ports; the main checkout manages the shared database instance.

## Build and verify

```bash
wails3 task dev
wails3 task check:frontend
wails3 task check:lint:server
wails3 task test:server
wails3 task build:server
wails3 task darwin:build ARCH=arm64
wails3 task windows:build ARCH=amd64
```

Run one server test suite at a time per worktree. Platform tasks manage target and CGO settings; select the architecture with `ARCH`.

## Contributing

Describe the behavior changed and the checks actually performed in each pull request. Update matching pages under `docs/en/` and `docs/zh-cn/` for product changes. Developer documentation is available at `/docs/en/developers/` when the server is running.

## License

[Apache License 2.0](LICENSE)
