.PHONY: all build test static clean run

# ── Local development ──────────────────────────────────────────────────────────

# Generate CSS and download JS, then run the server.
run: static
	go run ./cmd/server

# Build the server binary (requires static assets to exist first).
build: static
	go build -o bin/server ./cmd/server

# Run all tests with the race detector.
test:
	go test -race ./...

# Generate Tailwind CSS and copy htmx from node_modules.
# Run this once after cloning, and again after changing templates.
static: node_modules internal/assets/static/tailwind.min.css internal/assets/static/htmx.min.js

node_modules: package.json
	npm install
	@touch node_modules

internal/assets/static/tailwind.min.css: internal/assets/templates/*.html internal/assets/static/input.css tailwind.config.js
	node_modules/.bin/tailwindcss \
	  -i internal/assets/static/input.css \
	  -o internal/assets/static/tailwind.min.css \
	  --minify

internal/assets/static/htmx.min.js: node_modules
	cp node_modules/htmx.org/dist/htmx.min.js internal/assets/static/htmx.min.js

# ── Docker ─────────────────────────────────────────────────────────────────────

docker-build:
	docker build -t alltrails-to-gpx:latest .

docker-run: docker-build
	docker compose up

# ── Housekeeping ───────────────────────────────────────────────────────────────

clean:
	rm -f bin/server \
	      internal/assets/static/tailwind.min.css \
	      internal/assets/static/htmx.min.js
	rm -rf node_modules
