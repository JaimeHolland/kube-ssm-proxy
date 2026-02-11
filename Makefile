BINARY := kube-ssm-proxy
MODULE := kube-ssm-proxy

.PHONY: build clean tidy

build:
	go build -o $(BINARY) .

clean:
	rm -f $(BINARY)

tidy:
	go mod tidy
