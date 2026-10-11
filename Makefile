.PHONY: check spec-check

check: spec-check build test
	python3 scripts/check_project.py

spec-check:
	python3 scripts/check_spec.py
	python3 -m unittest discover -s scripts -p 'test_check_spec.py'

.PHONY: test
test:
	go test -race ./...
	go vet ./...
	python3 -m unittest discover -s minions -p 'test_*.py'

.PHONY: build run
build:
	mkdir -p build
	go build -o build/news-library ./cmd/news-library

run: build
	./build/news-library
