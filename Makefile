# Convenience targets. Every example also runs on its own: `cd NN-name && docker compose up -d --build`.
# EX is optional everywhere.
#
#   make start [EX=01]   start an example (no EX: pick one with fzf)
#   make stop  [EX=01]   stop and remove volumes (no EX: everything that's running)
#   make status [EX=01]  what's running
#   make logs  [EX=01]   fzf picker: choose containers, preview logs, ENTER to follow
#   make verify [EX=01]  smoke-test (MODE=demo, all assertions must pass; no EX: every example)

EX ?=

.PHONY: start stop status logs verify

start:
	@./scripts/start.sh $(EX)

stop:
	@./scripts/stop.sh $(EX)

status:
	@./scripts/status.sh $(EX)

logs:
	@./scripts/logs.sh $(EX)

verify:
	@./scripts/verify.sh $(EX)
