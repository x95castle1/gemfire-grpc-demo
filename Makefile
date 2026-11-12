# GemFire for gRPC feature tour. Run `make` (or `make help`) for the menu.
# Every example also runs on its own: `cd NN-name && docker compose up -d --build`.
.DEFAULT_GOAL := help

EX ?=
CLIENT ?=
SCRIPT ?=
RUN ?=
K6_ARGS ?=

.PHONY: help start stop status logs verify load results

help: ## show this menu
	@printf '\n\033[1mGemFire for gRPC: feature tour\033[0m\n\nUsage: make <target> [VAR=value ...]\n\n'
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} { printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2 }'
	@printf '\nVariables (all optional; anything left out is picked with fzf or means "everything"):\n'
	@printf '  \033[33m%-9s\033[0m %s\n' \
		EX      "example number, e.g. EX=01" \
		CLIENT  "load target: all | grpc-java | native-java" \
		SCRIPT  "k6 script under load-testing/scripts, e.g. SCRIPT=crud/steady" \
		RUN     "load: parallel | sequential   ·   results: latest | <run prefix>" \
		K6_ARGS "extra k6 args, e.g. K6_ARGS=\"-e RATE=3000 -e DURATION=1m\""
	@printf '\nExamples:\n'
	@ls -d [0-9][0-9]-*/ 2>/dev/null | sed 's|/$$||; s|^|  |'
	@printf '\n'

start: ## start an example (no EX: pick one)
	@./scripts/start.sh $(EX)

stop: ## stop an example and remove its volumes (no EX: everything running)
	@./scripts/stop.sh $(EX)

status: ## show what's running: containers, state, Grafana/Prometheus URLs
	@./scripts/status.sh $(EX)

logs: ## pick containers with fzf, preview their logs, ENTER to follow
	@./scripts/logs.sh $(EX)

verify: ## smoke-test: every client's demo must pass (no EX: every example)
	@./scripts/verify.sh $(EX)

load: ## k6 load test against an example's clients, then a native-vs-gRPC table
	@EX="$(EX)" CLIENT="$(CLIENT)" SCRIPT="$(SCRIPT)" RUN="$(RUN)" K6_ARGS='$(K6_ARGS)' ./scripts/load.sh

results: ## browse past load-test runs (RUN=latest prints the newest)
	@./scripts/results.sh $(RUN)
