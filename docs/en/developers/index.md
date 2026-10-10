---
title: Overview
order: 0
---

How to use the open API and webhooks.

This page is being written.

## Local database and tasks

Copy `.env.example` to `.env` from the repository root, run `wails3 task db:up` to start PostgreSQL, then run `wails3 task migrate` to create and migrate the current database. Defaults are `127.0.0.1:5432/main` for PostgreSQL and `http://localhost:8080/app/` for the web application.

Compose project `luway` gives containers and volumes their own namespace. The main checkout manages the instance. Additional worktrees configure distinct databases and server, Vite, and MCP ports in `.env`. `wails3 task test:server` recreates the database named with the `_test` suffix; run only one suite at a time per worktree.

The root Taskfile loads `.env`; shared tasks live in `build/tasks.yml` and platform tasks in `build/<platform>/Taskfile.yml`. Run builds and checks from the root through `wails3 task`.

## Desktop builds

Run `wails3 task dev` from the repository root to start desktop development on the current platform. Use the Go version specified in `go.mod` and a Wails CLI version matching its Wails dependency. Frontend builds require Node.js and npm.

The Windows build task disables CGO, so GCC is not required; running the Windows client requires WebView2. The macOS, Linux, iOS, and Android window systems require CGO and their platform development dependencies.

On Windows, run `wails3 task windows:build ARCH=amd64` to build the client, or `wails3 task windows:package ARCH=amd64 INSTALL_SCOPE=user` to create a per-user installer; packaging requires NSIS. Select other target architectures with `ARCH`; the tasks manage platform and CGO settings.

To end desktop development, press Ctrl+C in the terminal running `wails3 task dev`. The client and frontend development server exit together. Closing the client window hides it to the system tray; the tray menu's quit action exits the application. With the default npm configuration, Node starts Vite directly on Windows; other package managers use their own `dev` commands. Windows desktop builds require neither the Android SDK nor Unix command-line tools.

### Maintaining build tasks

`wails3 task common:update:build-assets` generates a complete React scaffold and build assets for the installed CLI in `.task/wails-reference-*`, together with its version. Before upgrading, read the target release notes, align the CLI, Go dependency, and frontend runtime to the same exact version, then generate reference files and manually merge relevant Taskfile and platform resource changes.

Bindings and frontend assets are regenerated for each build, once within a single task invocation. Desktop tasks select development mode with `DEV=true`; mobile tasks select production mode with `PRODUCTION=true`. Docker cross builds receive both the development mode and `APP_VERSION`. Linux packaging reads the executable from `OUTPUT` (defaulting to `BIN_DIR/application-filename`) and writes packages to `BIN_DIR`.

After changing platform tasks, run a native build on the corresponding system to validate the SDK, linker, and packages.

## Authentication

## Product documentation

Public sources live in `docs/zh-cn/` and `docs/en/`, with matching page paths. The sidebar uses `docs/nav.yaml`. Each page declares `title` and `order` in frontmatter. Use `{{product}}` for the deployment brand name and `{{slug}}` for the build identifier. Internal links use `/docs/<locale>/<page>/`.

`internal/productdocs.Load` reads the base filesystem and optional additional page sources, uses the base navigation, and rejects duplicate locale and page paths. The server injects extra sources through `serverapp.Extension.ProductDocs`. One `Site` supplies HTTP content, navigation, search, language switching, and `GetProductDocPage`. The loaded sources are the complete documentation for that build.

In-app help paths are registered in `frontend/src/lib/product-docs.ts`. When moving a page, update both languages, navigation, and help registrations. Server integration tests check paths and anchors; run `wails3 task common:build:frontend` to verify the frontend build.

## API reference

## Webhooks
