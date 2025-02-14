# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: autonity android ios autonity-cross evm all test clean lint lint-deps mock-gen test-fast
.PHONY: autonity-linux autonity-linux-386 autonity-linux-amd64 autonity-linux-mips64 autonity-linux-mips64le
.PHONY: autonity-linux-arm autonity-linux-arm-5 autonity-linux-arm-6 autonity-linux-arm-7 autonity-linux-arm64
.PHONY: autonity-darwin autonity-darwin-386 autonity-darwin-amd64
.PHONY: autonity-windows autonity-windows-386 autonity-windows-amd64

GOBIN = $(shell pwd)/build/bin
GO ?= latest
LATEST_COMMIT ?= $(shell git log -n 1 master --pretty=format:"%H")
ifeq ($(LATEST_COMMIT),)
LATEST_COMMIT := $(shell git log -n 1 HEAD~1 --pretty=format:"%H")
endif

autonity:
	build/env.sh go run build/ci.go install ./cmd/autonity
	@echo "Done building."
	@echo "Run \"$(GOBIN)/autonity\" to launch autonity."

all:
	build/env.sh go run build/ci.go install

android:
	build/env.sh go run build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/autonity.aar\" to use the library."

ios:
	build/env.sh go run build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/autonity.framework\" to use the library."

test: all
	build/env.sh go run build/ci.go test -coverage

test-fast:
	build/env.sh go run build/ci.go test

test-race-all: all
	build/env.sh go run build/ci.go test -race
	make test-race

test-race:
	go test -race -v ./consensus/tendermint/... -parallel 1
	go test -race -v ./consensus/test/... -timeout 30m

test-contracts:
	cd contracts/autonity/contract/ && truffle test && cd -

mock-gen:
	mockgen -source=consensus/tendermint/validator/validator_interface.go -package=validator -destination=consensus/tendermint/validator/validator_mock.go
	mockgen -source=consensus/tendermint/core/core_backend.go -package=core -destination=consensus/tendermint/core/backend_mock.go
	mockgen -source=consensus/protocol.go -package=consensus -destination=consensus/protocol_mock.go
	mockgen -source=consensus/consensus.go -package=consensus -destination=consensus/consensus_mock.go

lint:
	@echo "--> Running linter for code diff versus commit $(LATEST_COMMIT)"
	@./build/bin/golangci-lint run \
	    --new-from-rev=$(LATEST_COMMIT) \
	    --config ./.golangci/step1.yml \
	    --exclude "which can be annoying to use"

	@./build/bin/golangci-lint run \
	    --new-from-rev=$(LATEST_COMMIT) \
	    --config ./.golangci/step2.yml

	@./build/bin/golangci-lint run \
	    --new-from-rev=$(LATEST_COMMIT) \
	    --config ./.golangci/step3.yml

	@./build/bin/golangci-lint run \
	    --new-from-rev=$(LATEST_COMMIT) \
	    --config ./.golangci/step4.yml

lint-ci: lint-deps lint

test-deps:
	go get golang.org/x/tools/cmd/cover
	go get github.com/mattn/goveralls
	cd tests/testdata && git checkout 6b85703b568f4456582a00665d8a3e5c3b20b484

lint-deps:
	curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | sh -s -- -b ./build/bin v1.21.0

clean:
	./build/clean_go_build_cache.sh
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	go get -u github.com/golang/mock/mockgen
	env GOBIN= go get -u golang.org/x/tools/cmd/stringer
	env GOBIN= go get -u github.com/kevinburke/go-bindata/go-bindata
	env GOBIN= go get -u github.com/fjl/gencodec
	env GOBIN= go get -u github.com/golang/protobuf/protoc-gen-go
	env GOBIN= go install ./cmd/abigen
	@type "npm" 2> /dev/null || echo 'Please install node.js and npm'
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

# Cross Compilation Targets (xgo)

autonity-cross: autonity-linux autonity-darwin autonity-windows autonity-android autonity-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/autonity-*

autonity-linux: autonity-linux-386 autonity-linux-amd64 autonity-linux-arm autonity-linux-mips64 autonity-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-*

autonity-linux-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/autonity
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep 386

autonity-linux-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/autonity
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep amd64

autonity-linux-arm: autonity-linux-arm-5 autonity-linux-arm-6 autonity-linux-arm-7 autonity-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep arm

autonity-linux-arm-5:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/autonity
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep arm-5

autonity-linux-arm-6:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/autonity
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep arm-6

autonity-linux-arm-7:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/autonity
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep arm-7

autonity-linux-arm64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/autonity
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep arm64

autonity-linux-mips:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/autonity
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep mips

autonity-linux-mipsle:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/autonity
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep mipsle

autonity-linux-mips64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/autonity
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep mips64

autonity-linux-mips64le:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/autonity
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/autonity-linux-* | grep mips64le

autonity-darwin: autonity-darwin-386 autonity-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/autonity-darwin-*

autonity-darwin-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/autonity
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-darwin-* | grep 386

autonity-darwin-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/autonity
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-darwin-* | grep amd64

autonity-windows: autonity-windows-386 autonity-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/autonity-windows-*

autonity-windows-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/autonity
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-windows-* | grep 386

autonity-windows-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/autonity
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/autonity-windows-* | grep amd64
