BINARY := forum_forge
CMD := ./cmd/forum

.PHONY: build run test lint migrate-up migrate-down clean

build:
	go build -o $(BINARY) $(CMD)

run:
	go run $(CMD)

test:
	go test ./...

lint:
	golangci-lint run ./...

migrate-up:
	go run $(CMD) -migrate up

migrate-down:
	go run $(CMD) -migrate down

clean:
	rm -f $(BINARY)
