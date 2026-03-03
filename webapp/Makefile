.PHONY: build run clean dist reset

# Build the server binary
build:
	go build -o polis-server ./cmd/server

# Run the server (development)
run: build
	./polis-server

# Clean build artifacts
clean:
	rm -f polis-server
	rm -rf dist

# Create distribution bundle
dist: build
	mkdir -p dist/polis-app/data/keys
	mkdir -p dist/polis-app/data/drafts
	cp polis-server dist/polis-app/
	cp start.sh dist/polis-app/
	chmod +x dist/polis-app/start.sh
	@echo "Distribution created in dist/polis-app/"
	@echo "To run: cd dist/polis-app && ./start.sh"

# Reset distribution data (for testing setup flow)
reset:
	rm -rf dist/polis-app/data/keys/*
	rm -rf dist/polis-app/data/drafts/*
	rm -f dist/polis-app/data/.polis/webapp-config.json
	@echo "Distribution reset. Run ./start.sh to test setup flow again."
