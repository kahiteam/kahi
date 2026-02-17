FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOFIPS140=v1.0.0 \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -ldflags="-s -w -X github.com/kahiteam/kahi/internal/version.FIPS=true" \
    -o /kahi ./cmd/kahi

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /kahi /kahi
USER 65534:65534
ENTRYPOINT ["/kahi", "daemon"]
