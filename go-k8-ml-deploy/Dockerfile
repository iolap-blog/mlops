# syntax=docker/dockerfile:1

FROM golang:1.20 AS build-stage

ADD . /app
WORKDIR /app/server

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /docker-gs-ping

FROM build-stage AS run-test-stage
RUN go test -v ./...

FROM gcr.io/distroless/base-debian11 AS build-release-stage

WORKDIR /

COPY --from=build-stage /docker-gs-ping /docker-gs-ping

EXPOSE 3000

ENTRYPOINT ["/docker-gs-ping"]