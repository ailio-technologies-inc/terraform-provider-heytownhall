# Targets: build, install, fmt, lint, test, testacc, docs, release-check.
# `make testacc` needs the Terraform CLI on PATH (or TF_ACC_TERRAFORM_PATH set). It talks only to an in-process fake API.

.PHONY: testlive build install fmt lint test testacc docs docs-check release-check

build:
	go build -o terraform-provider-heytownhall .

install:
	go install .

fmt:
	gofmt -s -w .

lint:
	golangci-lint run ./...

# Unit tests, and the acceptance tests are skipped because TF_ACC is not set.
test:
	go test ./... -count=1 -timeout 5m

# Acceptance tests: real Terraform runs against the fake management API in internal/fakeapi.
testacc:
	TF_ACC=1 go test ./... -count=1 -timeout 30m -v

# Acceptance tests against a real management API: whichever HEYTOWNHALL_ENDPOINT and HEYTOWNHALL_MANAGEMENT_TOKEN name.
# They change that organisation (rename, spending cap, keys, invitations), so they also need HEYTOWNHALL_TEST_ALLOW_WRITES=1.
# Use a scratch organisation. See the header of internal/provider/live_test.go.
testlive:
	@test -n "$$HEYTOWNHALL_ENDPOINT" -a -n "$$HEYTOWNHALL_MANAGEMENT_TOKEN" || (echo "set HEYTOWNHALL_ENDPOINT and HEYTOWNHALL_MANAGEMENT_TOKEN"; exit 1)
	TF_ACC=1 go test ./internal/provider -run TestLive -count=1 -timeout 30m -v

# Regenerates docs/ from the schemas, examples/ and templates/. Needs the Terraform CLI on PATH.
docs:
	go generate ./...

# Fails when docs/ is out of date.
docs-check: docs
	git diff --exit-code -- docs

release-check:
	goreleaser check
