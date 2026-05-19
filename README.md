# http-probe-test-app

[![CI](https://github.com/cmontemuino/http-probe-test-app/actions/workflows/ci.yml/badge.svg)](https://github.com/cmontemuino/http-probe-test-app/actions/workflows/ci.yml)
[![CodeQL](https://github.com/cmontemuino/http-probe-test-app/workflows/CodeQL/badge.svg)](https://github.com/cmontemuino/http-probe-test-app/actions/workflows/codeql.yml)
[![Trivy](https://github.com/cmontemuino/http-probe-test-app/workflows/Trivy%20Container%20Scan/badge.svg)](https://github.com/cmontemuino/http-probe-test-app/security/code-scanning)
[![Renovate](https://img.shields.io/badge/renovate-enabled-brightgreen.svg)](https://renovatebot.com)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cmontemuino/http-probe-test-app/badge)](https://scorecard.dev/viewer/?uri=github.com/cmontemuino/http-probe-test-app)

A small HTTP service to **exercise Kubernetes liveness/readiness probes** and related monitoring:
artificial latency, configurable failure modes, and Prometheus metrics.

## Endpoints

| Endpoint | Method | Description |
|---|---:|---|
| `/` | GET | Returns a simple text response; increments metrics. |
| `/healthz` | GET | Liveness endpoint (200/500). Supports `?fail=1` override. |
| `/readyz` | GET | Readiness endpoint (200/503). Supports delay/toggle/thresholds. |
| `/toggle-ready` | POST | Toggles readiness state (affects `/readyz` and `/info`). |
| `/info` | GET | JSON info (version, commit, uptime, readiness, env). |
| `/metrics` | GET | Prometheus metrics. |

## Configuration (environment variables)

| Name | Default | Notes |
|---|---|---|
| `PORT` | `8080` | HTTP listen port. |
| `PREFIX` | `dummy` | Metrics prefix (e.g. `probe_test_requests_total`). |
| `CLUSTER_LABEL` | `unknown` | Exported as a metrics label and returned by `/info`. |
| `ROLLOUT_LABEL` | `v0` | Arbitrary rollout identifier returned by `/info` as `rollout_label`. Useful for tracking active rollout stages without modifying the container image. |
| `POD_NAME` | `unknown` | Metrics label + `/info`. |
| `NAMESPACE` | `unknown` | `/info`. |
| `NODE_NAME` | `unknown` | Metrics label + `/info`. |
| `ENVIRONMENT` | `development` | `/info`. |
| `EXTRA_LATENCY_MS` | `0` | Adds fixed latency to `/` responses. |
| `LATENCY_JITTER_MS` | `0` | Adds random extra latency (0..N) on top of `EXTRA_LATENCY_MS`. |
| `FAIL_LIVENESS_AFTER_N_REQUESTS` | `0` | If >0, `/healthz` returns 500 after N total requests. |
| `FAIL_READINESS_AFTER_N_REQUESTS` | `0` | If >0, `/readyz` returns 503 after N total requests. |
| `READY_DELAY_SECONDS` | `0` | If >0, `/readyz` returns 503 until delay elapses after start. |
| `SHUTDOWN_TIMEOUT_SECONDS` | `5` | Graceful shutdown timeout on SIGTERM/SIGINT. |

## Graceful Shutdown

The service handles `SIGTERM` and `SIGINT` signals for clean shutdown. When a signal is received:

1. The server stops accepting new connections
2. In-flight requests are allowed to complete (up to `SHUTDOWN_TIMEOUT_SECONDS`)
3. The process exits with code 0

This follows the Kubernetes pod termination lifecycle: kubelet sends SIGTERM, the app drains,
and kubelet sends SIGKILL after `terminationGracePeriodSeconds` (default 30s) if the process
is still running.

## Quick start (container)

```bash
docker run --rm -p 8080:8080 ghcr.io/cmontemuino/http-probe-test-app:latest
curl -sSf http://localhost:8080/healthz
```

## Local development

### Prerequisites

- [Go](https://go.dev/dl/) 1.25+
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) (for `make govulncheck`): `go install golang.org/x/vuln/cmd/govulncheck@latest`
- [golangci-lint](https://golangci-lint.run/welcome/install/) (for `make lint`)
- [Docker](https://docs.docker.com/get-docker/) (for `make docker-build`)

### Build, test, lint

```bash
make build
make test
make lint
```

## Docker

```bash
docker build -t http-probe-test-app:local .
docker run --rm -p 8080:8080 http-probe-test-app:local
```

## Kubernetes

See `deploy/` for a minimal `Deployment` and `Service` you can apply to a cluster.

## Supply Chain Security

Every released container image includes cryptographically signed attestations:

| Attestation | Format | Purpose |
|-------------|--------|---------|
| **SLSA Build Provenance** | [in-toto v1](https://in-toto.io/) | Proves the image was built by this repository's CI |
| **SBOM** | [SPDX 2.3](https://spdx.dev/) (JSON) | Lists all packages in the image |

Attestations are signed with [Sigstore](https://www.sigstore.dev/) via GitHub's OIDC provider (no manual keys).
They are stored both in GHCR (as OCI artifacts) and via the [GitHub Attestations API](https://github.com/cmontemuino/http-probe-test-app/attestations).

### Verify an image

```bash
gh attestation verify oci://ghcr.io/cmontemuino/http-probe-test-app:v0.3.0 \
  --owner cmontemuino
```

Expected output:
```
Loaded digest sha256:... for oci://ghcr.io/cmontemuino/http-probe-test-app:v0.3.0
Loaded 2 attestations from ghcr.io/cmontemuino/http-probe-test-app
✓ Verification succeeded!
```

### Download attestations

```bash
# Build provenance
gh attestation download oci://ghcr.io/cmontemuino/http-probe-test-app:v0.3.0 \
  --owner cmontemuino \
  --predicate-type https://slsa.dev/provenance/v1

# SBOM
gh attestation download oci://ghcr.io/cmontemuino/http-probe-test-app:v0.3.0 \
  --owner cmontemuino \
  --predicate-type https://spdx.dev/Document
```

### Inspect attestations online

- [GitHub Attestations](https://github.com/cmontemuino/http-probe-test-app/attestations)
- [Sigstore Transparency Log](https://search.sigstore.dev/)

### SLSA Level

This project achieves [SLSA Level 3](https://slsa.dev/spec/v1.0/levels) for container images:
- **Level 1**: Documented build process (Dockerfile, CI/CD)
- **Level 2**: Tamper-resistant build (GitHub Actions)
- **Level 3**: Hardened build platform (GitHub-hosted runners, non-forgeable provenance)

### Policy enforcement

For production Kubernetes clusters, enforce provenance verification before admitting images:
- [Sigstore Policy Controller](https://docs.sigstore.dev/policy-controller/overview/)
- [Kyverno](https://kyverno.io/)
- [Ratify](https://ratify.dev/)

## Security

This project implements comprehensive security scanning:

- **CodeQL**: Automated code security analysis
- **Trivy**: Container vulnerability scanning
- **govulncheck**: Go vulnerability database checks
- **Renovate**: Automated dependency updates
- **Dependabot**: Security-focused alerts

Security findings are tracked in the [Security tab](https://github.com/cmontemuino/http-probe-test-app/security).

See [SECURITY.md](SECURITY.md) for vulnerability reporting and security policies.

## License

Apache-2.0. See `LICENSE`.
