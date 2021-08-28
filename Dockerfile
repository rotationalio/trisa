FROM golang:1.16 AS builder

WORKDIR /srv/build

COPY . .
RUN go test ./... && go build -v ./cmd/trisarl

FROM ubuntu:bionic

LABEL maintainer="Rotational Labs <info@rotational.io>"
LABEL description="Rotational Labs TRISA Node Implementation"

RUN apt-get update && apt-get install -y ca-certificates
RUN apt-get update && apt-get install -y wget gnupg

COPY --from=builder /srv/build/trisarl /bin/

ENV TRISA_BIND_ADDR=":443"
ENV TRISA_MAINTENANCE="false"
ENV TRISA_DIRECTORY_ADDR="api.trisatest.net:443"
ENV TRISA_SERVER_CERTS=""
ENV TRISA_SERVER_CERTPOOL=""
ENV TRISA_LOG_LEVEL="info"
ENV TRISA_CONSOLE_LOG="true"

ENTRYPOINT [ "/bin/trisarl", "serve" ]