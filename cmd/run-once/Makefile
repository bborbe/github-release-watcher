include ../../../../Makefile.variables

# Override on the command line:
#   make run-once REPO_ALLOWLIST=github.com/owner/repo
OWNER           ?= bborbe
REPO_ALLOWLIST  ?=
STAGE           ?= dev
CURSOR_PATH     ?= /tmp/cursor.json

run-once:
	@GH_TOKEN=$$(gh auth token) go run -mod=mod . \
		-kafka-brokers="$(KAFKA_BROKERS)" \
		-stage=$(STAGE) \
		-owner=$(OWNER) \
		-repo-allowlist="$(REPO_ALLOWLIST)" \
		-cursor-path="$(CURSOR_PATH)" \
		-v=2