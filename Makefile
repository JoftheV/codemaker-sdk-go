protoc:
	protoc ./proto/codemakerai.proto --go_out=./ --go-grpc_out=./

clean:
	go clean
	rm -f ./codemaker-sdk-go

compile:
	go build ./...

test:
	go test ./...

build: clean protoc compile test
	@: