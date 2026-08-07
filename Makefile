.PHONY: web-build

# Builds the Vite/React web UI and copies it over internal/web/webui/, the
# go:embed target in internal/web/server.go. The output is committed to git
# (not gitignored): `go install github.com/voocel/ainovel-cli/cmd/ainovel-cli@latest`
# and any other build-from-source path must work with no Node/npm available,
# so a ready-to-serve build has to already be on disk at go-build time.
# Run this after any change under internal/web/webui-src/ and commit the result.
web-build:
	cd internal/web/webui-src && npm ci && npm run build
	rm -rf internal/web/webui
	cp -r internal/web/webui-src/dist internal/web/webui
