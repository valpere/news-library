.PHONY: check spec-check

check: spec-check test
	python3 scripts/check_project.py

spec-check:
	python3 scripts/check_spec.py
	python3 -m unittest discover -s scripts -p 'test_check_spec.py'

.PHONY: test
test:
	go test -race ./...
	go vet ./...
