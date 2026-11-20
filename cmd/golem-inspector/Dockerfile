# Golem Inspector hub image. Build from the repository root:
#
#   docker build -f cmd/golem-inspector/Dockerfile -t golem-inspector .
#
# Configure it with GOLEM_INSPECTOR_* variables (see golem-inspector -h).

FROM node:24-alpine AS web
WORKDIR /src
COPY golem-inspector/package.json golem-inspector/package-lock.json golem-inspector/
RUN cd golem-inspector && npm ci --no-audit --no-fund
COPY golem-inspector/ golem-inspector/
COPY cmd/golem-inspector/web/ cmd/golem-inspector/web/
RUN cd golem-inspector && npm run build

FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Only the packages the hub links; the hub never imports the game runtime.
COPY golem/pb/ golem/pb/
COPY golem/inspector/wire/ golem/inspector/wire/
COPY golem/inspector/hub/ golem/inspector/hub/
COPY cmd/golem-inspector/ cmd/golem-inspector/
COPY --from=web /src/cmd/golem-inspector/web/dist/ cmd/golem-inspector/web/dist/
RUN CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/golem-inspector ./cmd/golem-inspector

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/golem-inspector /golem-inspector
ENV GOLEM_INSPECTOR_ADDR=:8787
EXPOSE 8787
USER nonroot
ENTRYPOINT ["/golem-inspector"]
