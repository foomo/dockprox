.DEFAULT_GOAL:=help
-include .makerc

# --- Config -----------------------------------------------------------------

# Newline hack for error output
define br


endef

# --- Targets -----------------------------------------------------------------

# This allows us to accept extra arguments
%: .mise .lefthook
	@:

.PHONY: .mise
# Install dependencies
.mise:
ifeq (, $(shell command -v mise))
	$(error $(br)$(br)Please ensure you have 'mise' installed and activated!$(br)$(br)  $$ brew update$(br)  $$ brew install mise$(br)$(br)See the documentation: https://mise.jdx.dev/getting-started.html)
endif
	@mise install

.PHONY: .lefthook
# Configure git hooks for lefthook
.lefthook:
	@lefthook install --reset-hooks-path

### Tasks

.PHONY: check
## Run lint & tests
check: tidy generate lint.fix test.race audit

.PHONY: lint
## Run linter
lint:
	@echo "〉golangci-lint run"
	golangci-lint run --max-same-issues 0 --max-issues-per-linter 0

.PHONY: lint.fix
## Fix lint violations
lint.fix:
	@echo "〉golangci-lint run fix"
	golangci-lint run --fix --max-same-issues 0 --max-issues-per-linter 0

.PHONY: generate
## Run go generate
generate:
	@echo "〉go generate"
	@go generate ./...

.PHONY: test
## Run tests
test:
	@echo "〉go test"
	@GO_TEST_TAGS=-skip go test -coverprofile=coverage.out -tags=safe ./...

.PHONY: test.race
## Run tests with -race
test.race:
	@echo "〉go test -race"
	@GO_TEST_TAGS=-skip go test -coverprofile=coverage.out -tags=safe -race ./...

.PHONY: build
## Build binary
build:
	@echo "〉go build bin/dockprox"
	@rm -f bin/dockprox
	@go build -o bin/dockprox cmd/dockprox/dockprox.go

.PHONY: build.menubar
## Build macOS menubar app binary (darwin only)
build.menubar: VERSION ?= $(shell git describe --tags --always --dirty)
build.menubar:
ifeq ($(shell uname),Darwin)
	@echo "〉go build bin/dockprox-menubar"
	@rm -f bin/dockprox-menubar
	@go build -tags=safe -ldflags "-X github.com/foomo/dockprox/internal/menubar.version=$(VERSION)" -o bin/dockprox-menubar ./cmd/dockprox-menubar
else
	$(error build.menubar requires macOS)
endif

.PHONY: package.menubar
## Package macOS menubar app into dist/Dockprox.app (darwin only)
package.menubar: VERSION ?= $(shell git describe --tags --always --dirty)
package.menubar: build.menubar
ifeq ($(shell uname),Darwin)
	@echo "〉packaging dist/Dockprox.app ($(VERSION))"
	@rm -rf dist/Dockprox.app
	@mkdir -p dist/Dockprox.app/Contents/MacOS dist/Dockprox.app/Contents/Resources
	@iconutil -c icns cmd/dockprox-menubar/icon.iconset -o dist/Dockprox.app/Contents/Resources/icon.icns
	@sed 's/__VERSION__/$(VERSION)/g' cmd/dockprox-menubar/Info.plist > dist/Dockprox.app/Contents/Info.plist
	@cp bin/dockprox-menubar dist/Dockprox.app/Contents/MacOS/dockprox-menubar
	@codesign --force --deep --sign - dist/Dockprox.app
else
	$(error package.menubar requires macOS)
endif

.PHONY: build.debug
## Build binary in debug mode
build.debug:
	@echo "〉go build bin/dockprox (debug)"
	@rm -f bin/dockprox
	@go build -gcflags "all=-N -l" -o bin/dockprox cmd/dockprox/dockprox.go

.PHONY: install
## Run go install
install:
	@echo "〉installing dockprox"
	@go install cmd/dockprox/dockprox.go

.PHONY: install.debug
## Run go install with debug
install.debug:
	@echo "〉installing dockprox (debug)"
	@go install -gcflags "all=-N -l" cmd/dockprox/dockprox.go

### Security

.PHONY: audit
## Run security audit
audit:
	@echo "〉security audit"
	@go install golang.org/x/vuln/cmd/govulncheck@latest
	@govulncheck ./...

### Dependencies

.PHONY: tidy
## Run go mod tidy
tidy:
	@echo "〉go mod tidy"
	@go mod tidy

.PHONY: outdated
## Show outdated direct dependencies
outdated:
	@echo "〉go mod outdated"
	@GOWORK=off go-mod-upgrade --list

.PHONY: upgrade
## Upgrade direct dependencies
upgrade:
	@echo "〉go mod upgrade"
	@GOWORK=off go-mod-upgrade
	@$(MAKE) tidy

### Documentation

.PHONY: docs.cli
## Generate CLI markdown reference (docs/reference/cli)
docs.cli:
	@echo "〉generate cli docs"
	@rm -rf docs/reference/cli
	@mkdir -p docs/reference/cli
	@go run ./cmd/dockprox-docs --out docs/reference/cli

.PHONY: docs.schema
## Generate JSON schema (dockprox.schema.json)
docs.schema:
	@echo "〉generate dockprox.schema.json"
	@go run ./cmd/dockprox-schema --out dockprox.schema.json

.PHONY: docs
## Open docs
docs: docs.cli docs.schema
	@echo "〉starting docs"
	@cd docs && bun install && bun run dev

.PHONY: docs.build
## Build docs site
docs.build: docs.cli docs.schema
	@echo "〉building docs"
	@cd docs && bun install && bun run build

.PHONY: godocs
## Open go docs
godocs:
	@echo "〉starting go docs"
	@go doc -http

### Utils

.PHONY: help
# https://patorjk.com/software/taag/#p=display&f=Tmplr&t=dockprox&x=none&v=4&h=4&w=80&we=false
## Show help text
help: g=\033[0;32m
help: b=\033[0;34m
help: w=\033[0;90m
help: e=\033[0m
help:
	@echo "$(g)"
	@echo " ┓   ┓"
	@echo "┏┫┏┓┏┃┏┏┓┏┓┏┓┓┏"
	@echo "┗┻┗┛┗┛┗┣┛┛ ┗┛┛┗"
	@echo "       ┛"
	@echo "with ❤ foomo by bestbytes"
	@echo "$(e)"
	@echo "$(b)Usage:$(e)\n  make [task]"
	@awk '{ \
		if($$0 ~ /^### /){ \
			if(help) printf "  %-21s $(w)%s$(e)\n\n", cmd, help; help=""; \
			printf "$(b)\n%s:$(e)\n", substr($$0,5); \
		} else if($$0 ~ /^[a-zA-Z0-9._-]+:/){ \
			cmd = substr($$0, 1, index($$0, ":")-1); \
			if(help) printf "  %-21s $(w)%s$(e)\n", cmd, help; help=""; \
		} else if($$0 ~ /^##/){ \
			help = help ? help "\n                        " substr($$0,3) : substr($$0,3); \
		} else if(help){ \
			print "\n                        $(w)" help "$(e)\n"; help=""; \
		} \
	}' $(MAKEFILE_LIST)
	@echo ""

