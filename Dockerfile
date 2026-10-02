FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/radaro ./cmd/radaro

FROM alpine:3.24
RUN apk add --no-cache ca-certificates chromium font-noto xvfb-run tini && adduser -D -u 10001 radaro && mkdir /data && chown radaro /data
USER radaro
ENV RADARO_DB=/data/radaro.db
ENV RADARO_BROWSER_PATH=/usr/bin/chromium-browser RADARO_BROWSER_NO_SANDBOX=true
ENV RADARO_BROWSER_HEADED=true
VOLUME /data
EXPOSE 8042
COPY --from=build /out/radaro /usr/local/bin/radaro
ENTRYPOINT ["tini", "-g", "--", "xvfb-run", "-a", "-s", "-screen 0 1000x720x24 -nolisten tcp", "radaro"]
CMD ["serve", "--host", "0.0.0.0"]
