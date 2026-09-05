FROM golang:1.26.8-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends screen openssh-server openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/sshd \
    && useradd --uid 1000 --create-home --shell /bin/sh sclaude-test \
    && passwd -d sclaude-test
WORKDIR /dependency-cache
COPY go.mod go.sum ./
RUN go mod download && chmod -R a+rX /go/pkg/mod
WORKDIR /src
