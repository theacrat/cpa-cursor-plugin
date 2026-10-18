.PHONY: check fmt fmt-check scripts-check test test-race test-abi test-host vet build hooks build-linux-arm64 build-linux-amd64 build-darwin-arm64 build-darwin-amd64

check fmt fmt-check scripts-check test test-race test-abi test-host vet build hooks build-linux-arm64 build-linux-amd64 build-darwin-arm64 build-darwin-amd64:
	mise run $@
