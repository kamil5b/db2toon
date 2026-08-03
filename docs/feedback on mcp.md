Your existing workflow can keep producing native release binaries. Add two jobs:

1. Build and push a multi-platform `db2toon-mcp` image to GHCR.
2. Update `server.json` for the tag and publish it using GitHub OIDC.

The registry verifies that the OCI image exists and contains an `io.modelcontextprotocol.server.name` label matching `server.json`. ([Model Context Protocol][1]) GitHub Actions publishing also requires `packages: write`, while MCP Registry OIDC authentication requires `id-token: write`. ([GitHub Docs][2])

## Revised workflow

```yaml
name: Build

on:
  push:
    tags:
      - "v*"
  workflow_dispatch:

permissions:
  contents: write
  packages: write
  id-token: write

jobs:
  test:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Unit tests
        env:
          CGO_ENABLED: 0
        run: go test ./...

      - name: Integration tests
        env:
          CGO_ENABLED: 0
        run: |
          go test -tags=integration \
            ./internal/database/postgres \
            ./internal/mcp

  build:
    needs: test
    runs-on: ubuntu-latest

    strategy:
      matrix:
        include:
          - goos: linux
            goarch: amd64
          - goos: linux
            goarch: arm64
          - goos: windows
            goarch: amd64
          - goos: windows
            goarch: arm64
          - goos: darwin
            goarch: amd64
          - goos: darwin
            goarch: arm64

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Build
        env:
          CGO_ENABLED: 0
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: |
          mkdir -p dist

          for command in db2toon pg2toon db2toon-mcp; do
            binary_name="${command}"

            if [ "${{ matrix.goos }}" = "windows" ]; then
              binary_name="${binary_name}.exe"
            fi

            go build \
              -trimpath \
              -ldflags="-s -w" \
              -o "dist/${binary_name}" \
              "./cmd/${command}"

            final_name="${{ matrix.goarch }}-${{ matrix.goos }}-${binary_name}"
            mv "dist/${binary_name}" "dist/${final_name}"

            sha256sum "dist/${final_name}" \
              > "dist/${final_name}.sha256"
          done

      - name: Upload to Release
        if: startsWith(github.ref, 'refs/tags/v')
        uses: softprops/action-gh-release@v2
        with:
          files: dist/*
          fail_on_unmatched_files: true

  publish-image:
    name: Publish MCP container
    needs: test
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest

    outputs:
      image: ${{ steps.image.outputs.name }}

    steps:
      - uses: actions/checkout@v4

      - name: Determine image name
        id: image
        shell: bash
        run: |
          image="ghcr.io/${GITHUB_REPOSITORY,,}"
          echo "name=${image}" >> "$GITHUB_OUTPUT"

      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push MCP image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile.mcp
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            ${{ steps.image.outputs.name }}:${{ github.ref_name }}
            ${{ steps.image.outputs.name }}:latest
          labels: |
            org.opencontainers.image.source=${{ github.server_url }}/${{ github.repository }}
            org.opencontainers.image.revision=${{ github.sha }}
            org.opencontainers.image.version=${{ github.ref_name }}
            io.modelcontextprotocol.server.name=io.github.kamil5b/db2toon-mcp

  publish-registry:
    name: Publish to MCP Registry
    needs: publish-image
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest

    permissions:
      contents: read
      id-token: write

    steps:
      - uses: actions/checkout@v4

      - name: Prepare server.json
        env:
          IMAGE: ${{ needs.publish-image.outputs.image }}
          TAG: ${{ github.ref_name }}
        run: |
          VERSION="${TAG#v}"

          jq \
            --arg version "$VERSION" \
            --arg identifier "${IMAGE}:${TAG}" \
            '
              .version = $version
              | .packages[0].registryType = "oci"
              | .packages[0].identifier = $identifier
            ' \
            server.json > server.tmp.json

          mv server.tmp.json server.json
          cat server.json

      - name: Install mcp-publisher
        run: |
          curl -L \
            "https://github.com/modelcontextprotocol/registry/releases/latest/download/mcp-publisher_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" \
            | tar xz mcp-publisher

          chmod +x mcp-publisher

      - name: Authenticate to MCP Registry
        run: ./mcp-publisher login github-oidc

      - name: Publish to MCP Registry
        run: ./mcp-publisher publish
```

The official workflow documentation recommends `github-oidc` for GitHub Actions, which requires no dedicated registry secret. ([GitHub][3])

## `Dockerfile.mcp`

Create this at the repository root:

```dockerfile
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 \
    GOOS="${TARGETOS}" \
    GOARCH="${TARGETARCH}" \
    go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/db2toon-mcp \
      ./cmd/db2toon-mcp

FROM alpine:3.22

RUN addgroup -S mcp && adduser -S -G mcp mcp

COPY --from=build \
  /out/db2toon-mcp \
  /usr/local/bin/db2toon-mcp

LABEL io.modelcontextprotocol.server.name="io.github.kamil5b/db2toon-mcp"

USER mcp

ENTRYPOINT ["/usr/local/bin/db2toon-mcp"]
```

The label appears in both the Dockerfile and build workflow intentionally. Either location works, and keeping it in the Dockerfile ensures locally built images also contain the ownership metadata.

## `server.json`

Commit a base file resembling:

```json
{
  "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  "name": "io.github.kamil5b/db2toon-mcp",
  "title": "DB2TOON MCP",
  "description": "MCP server for working with PostgreSQL databases and DB2TOON",
  "version": "0.0.0",
  "packages": [
    {
      "registryType": "oci",
      "identifier": "ghcr.io/kamil5b/db2toon:v0.0.0",
      "transport": {
        "type": "stdio"
      }
    }
  ]
}
```

The workflow replaces `version` and `identifier` before publication. For tag `v1.2.3`, it publishes metadata equivalent to:

```json
{
  "version": "1.2.3",
  "packages": [
    {
      "registryType": "oci",
      "identifier": "ghcr.io/kamil5b/db2toon:v1.2.3",
      "transport": {
        "type": "stdio"
      }
    }
  ]
}
```

OCI packages on GHCR are supported by the MCP Registry, and the package must be publicly accessible for registry consumers. ([Model Context Protocol][4])

## Release it

```bash
git add .github/workflows/build.yml Dockerfile.mcp server.json
git commit -m "Publish MCP server through GHCR"
git push

git tag v1.0.0
git push origin v1.0.0
```

After the first run, check the package under your GitHub profile and ensure its visibility is **Public**. GHCR packages may inherit repository permissions when linked to the repository, but package visibility should still be verified explicitly. ([GitHub Docs][5])

[1]: https://modelcontextprotocol.io/registry/package-types?utm_source=chatgpt.com "MCP Registry Supported Package Types"
[2]: https://docs.github.com/en/packages/managing-github-packages-using-github-actions-workflows/publishing-and-installing-a-package-with-github-actions?utm_source=chatgpt.com "Publishing and installing a package with GitHub Actions"
[3]: https://github.com/modelcontextprotocol/registry/blob/main/docs/modelcontextprotocol-io/github-actions.mdx?utm_source=chatgpt.com "github-actions.mdx - modelcontextprotocol/registry"
[4]: https://modelcontextprotocol.io/registry/about?utm_source=chatgpt.com "The MCP Registry"
[5]: https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility?utm_source=chatgpt.com "Configuring a package's access control and visibility"
