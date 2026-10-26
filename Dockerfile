FROM golang:1.26.0-alpine3.23 AS dependencies
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
FROM dependencies AS tools
# Go integration tools keep SQL fixtures, migrations, scripts and documentation.
# Generated evidence stays on the host or an explicit evidence mount.
COPY . .
FROM dependencies AS build
# Runtime compilation depends only on Go sources, including embedded SQL.
# Reports, evidence and tool-only files cannot invalidate this layer.
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
 CGO_ENABLED=0 go build -trimpath -o /out/seed ./cmd/seed && \
 CGO_ENABLED=0 go build -trimpath -o /out/simulator ./cmd/simulator && \
 CGO_ENABLED=0 go build -trimpath -o /out/verify ./cmd/verify
FROM alpine:3.23.3 AS runtime
RUN addgroup -g 10001 app && adduser -D -u 10001 -G app app
RUN mkdir /state && chown app:app /state
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations /app/migrations
USER 10001:10001
CMD ["/app/api"]
