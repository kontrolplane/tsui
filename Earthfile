VERSION 0.8
FROM golang:1.26-alpine
WORKDIR /kontrolplane

deps:
    COPY go.mod go.sum ./
    RUN go mod download

compile:
    FROM +deps
    COPY main.go .
    COPY cmd/ cmd/
    COPY pkg/ pkg/
    ARG VERSION=dev
    RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o build/kontrolplane/tsui .
    SAVE ARTIFACT build/kontrolplane/tsui AS LOCAL build/kontrolplane/tsui

container:
    ARG VERSION=dev
    FROM DOCKERFILE --build-arg VERSION=${VERSION} .
    ARG tag="latest"
    SAVE IMAGE ghcr.io/kontrolplane/tsui:${tag}

seed:
    LOCALLY
    ARG NATS_URL="nats://localhost:4222"
    RUN NATS_URL=$NATS_URL bash seed/seed.sh

nats:
    LOCALLY
    ARG NATS_URL="nats://localhost:4222"
    RUN docker compose up -d
    RUN for i in $(seq 1 30); do nats --server $NATS_URL account info >/dev/null 2>&1 && exit 0; sleep 1; done; echo "nats did not become ready" && exit 1

dev:
    LOCALLY
    ARG NATS_URL="nats://localhost:4222"
    WAIT
        BUILD +nats
    END
    RUN NATS_URL=$NATS_URL bash seed/seed.sh
    RUN go build -o build/kontrolplane/tsui .

list:
    LOCALLY
    ARG NATS_URL="nats://localhost:4222"
    RUN nats --server $NATS_URL stream ls

vhs:
    LOCALLY
    ARG NATS_URL="nats://localhost:4222"
    RUN NATS_URL=$NATS_URL vhs vhs/cassette.tape

all:
  BUILD +compile
  BUILD +container
