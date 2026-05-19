# Build stage
ARG GO_IMAGE=golang:1.25.10-trixie
FROM ${GO_IMAGE} AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s -X 'main.Version=${VERSION}' -X 'main.GitCommit=${GIT_COMMIT}'" -o http-probe-test-app .

# Runtime stage
FROM scratch

LABEL org.opencontainers.image.source="https://github.com/cmontemuino/http-probe-test-app"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY --from=builder /app/http-probe-test-app /http-probe-test-app

EXPOSE 8080

ENTRYPOINT ["/http-probe-test-app"]
