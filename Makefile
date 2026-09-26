BINARY := arabic-vocab
VENV   := .venv
PYTHON := $(VENV)/bin/python

.PHONY: build test vet fmt lint update venv deck clean completions

build:
	go build -o $(BINARY) ./cmd/arabic-vocab

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet
	gofmt -l .

update:
	go get github.com/scuba-plaza/arabic-tts@latest
	go mod tidy

venv:
	python3 -m venv $(VENV)
	$(PYTHON) -m pip install -r scripts/requirements.txt
	$(VENV)/bin/camel_data -i morphology-db-msa-r13
	$(VENV)/bin/camel_data -i disambig-mle-calima-msa-r13
	$(VENV)/bin/camel_data -i disambig-bert-unfactored-msa

deck: build
	./$(BINARY) check
	./$(BINARY) audio
	./$(BINARY) build

clean:
	rm -f $(BINARY)
	rm -rf out

COMPDIR := $(HOME)/.local/share/zsh/site-functions

completions: build
	@mkdir -p $(COMPDIR)
	./$(BINARY) completion zsh > $(COMPDIR)/_$(BINARY)
	@rm -f $${ZDOTDIR:-$$HOME}/.zcompdump
	@echo "installed $(COMPDIR)/_$(BINARY) — restart zsh to pick it up"