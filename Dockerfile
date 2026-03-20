# ── Stage 1: Build CSS and download JS assets ─────────────────────────────────
FROM node:22-alpine AS asset-builder

WORKDIR /app

COPY package.json ./
RUN npm install

COPY tailwind.config.js ./
COPY internal/assets/templates/ internal/assets/templates/
COPY internal/assets/static/input.css internal/assets/static/input.css

RUN node_modules/.bin/tailwindcss \
      -i internal/assets/static/input.css \
      -o internal/assets/static/tailwind.min.css \
      --minify && \
    cp node_modules/htmx.org/dist/htmx.min.js internal/assets/static/htmx.min.js

# ── Stage 2: Build Rust binary ─────────────────────────────────────────────────
FROM rust:1-slim AS rust-builder

# Pin the version and use --locked so transitive deps can't drift.
# Update this pin when upgrading alltrailsgpx.
RUN cargo install alltrailsgpx --version 0.2.0 --locked

# ── Stage 3: Build Go binary ───────────────────────────────────────────────────
FROM golang:1.26-alpine AS go-builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Inject the generated assets before building so //go:embed picks them up.
COPY --from=asset-builder /app/internal/assets/static/tailwind.min.css internal/assets/static/tailwind.min.css
COPY --from=asset-builder /app/internal/assets/static/htmx.min.js       internal/assets/static/htmx.min.js

RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -trimpath -o /server ./cmd/server

# ── Stage 4: Runtime ───────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*

COPY --from=rust-builder /usr/local/cargo/bin/alltrailsgpx /usr/local/bin/alltrailsgpx
COPY --from=go-builder   /server                           /server

EXPOSE 8080

USER 65534:65534

ENTRYPOINT ["/server"]
