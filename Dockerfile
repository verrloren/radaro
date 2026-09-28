FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/radaro ./cmd/radaro

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 radaro && mkdir /data && chown radaro /data
USER radaro
ENV RADARO_DB=/data/radaro.db
VOLUME /data
EXPOSE 8042
COPY --from=build /out/radaro /usr/local/bin/radaro
ENTRYPOINT ["radaro"]
CMD ["serve", "--host", "0.0.0.0"]
