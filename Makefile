.PHONY: run build pi clean

# Run locally on macOS
run:
	AEROAPI_KEY=nil2 go run ./cmd/flighttracker

# Build for local machine
build:
	go build -ldflags="-s -w" -o flighttracker ./cmd/flighttracker

# Cross-compile for Raspberry Pi Zero 2 W (ARM64 Linux)
pi:
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o flighttracker-pi ./cmd/flighttracker

# Clean build artifacts
clean:
	rm -f flighttracker flighttracker-pi
