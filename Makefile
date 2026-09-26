BINARY := arabic-vocab
VENV   := .venv
PYTHON := $(VENV)/bin/python

.PHONY: build test vet fmt lint update venv camel-lemmas deck clean

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

camel-lemmas:
	$(PYTHON) scripts/camel_lemmas.py deck-data/camel-lemmas.tsv deck-data/raw/subtitles-ar-50k.txt:50000 deck-data/raw/camel-msa-top.tsv:100000

deck: build
	./$(BINARY) check
	./$(BINARY) audio
	./$(BINARY) build

clean:
	rm -f $(BINARY)
	rm -rf out
