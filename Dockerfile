FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/radaro ./cmd/radaro

FROM alpine:3.24 AS codex
ARG TARGETARCH
RUN apk add --no-cache curl && mkdir /codex && \
    case "${TARGETARCH:-amd64}" in \
      amd64) target=x86_64; sha=306865417d4ee7a927785852910a527f41e1e159add390ac5ae3accb67d44a13 ;; \
      arm64) target=aarch64; sha=883620139925f677e5a12c95ba87a1d7f421388ebb797b1c10aba42a04ede9d7 ;; \
      *) exit 1 ;; \
    esac && \
    curl -fsSL --retry 3 "https://github.com/openai/codex/releases/download/rust-v0.160.0/codex-${target}-unknown-linux-musl.tar.gz" -o /tmp/codex.tar.gz && \
    echo "$sha  /tmp/codex.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/codex.tar.gz -C /codex && \
    mv "/codex/codex-${target}-unknown-linux-musl" /codex/codex

FROM alpine:3.24
RUN apk add --no-cache ca-certificates chromium font-noto xvfb-run tini && adduser -D -u 10001 radaro && mkdir /data && chown radaro /data
USER radaro
ENV RADARO_DB=/data/radaro.db
ENV RADARO_BROWSER_PATH=/usr/bin/chromium-browser RADARO_BROWSER_NO_SANDBOX=true
ENV RADARO_BROWSER_HEADED=true
ENV RADARO_CODEX_HOME=/data/codex
VOLUME /data
EXPOSE 8042
COPY --from=build /out/radaro /usr/local/bin/radaro
COPY --from=codex /codex/codex /usr/local/bin/codex
COPY third_party/codex/ /usr/local/share/licenses/openai-codex/
ENTRYPOINT ["tini", "-g", "--", "xvfb-run", "-a", "-s", "-screen 0 1000x720x24 -nolisten tcp", "radaro"]
CMD ["serve", "--host", "0.0.0.0"]
