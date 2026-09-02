FROM registry.access.redhat.com/ubi9/go-toolset:1.26 AS builder

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/

USER root
RUN GO111MODULE=on CGO_ENABLED=1 GOOS=linux GOEXPERIMENT=strictfipsruntime \
    go build -tags 'strictfipsruntime netgo' -a -o /bin/cmd ./cmd/main.go

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

COPY --from=builder /bin/cmd /bin/cmd

USER 65532:65532

ENTRYPOINT ["/bin/cmd"]
