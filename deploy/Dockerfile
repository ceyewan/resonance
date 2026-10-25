FROM node:22-alpine AS frontend-builder
WORKDIR /src
COPY api/gen/ts ./api/gen/ts
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.26.10 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/resonance .

FROM alpine:3.20 AS final
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/bin/resonance /app/resonance
COPY --from=builder /app/configs /app/configs
COPY --from=frontend-builder /src/web/dist /app/web/dist
WORKDIR /app
USER nobody
ENTRYPOINT ["/app/resonance"]

# One Pilot container: Go control and pinned Pi subprocess; no business tools.
FROM node:22-alpine AS pilot-final
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 resonance \
    && adduser -S -D -H -u 10001 -G resonance resonance \
    && mkdir -p /app /opt/resonance/bridge /var/lib/resonance-pilot \
    && chown -R resonance:resonance /var/lib/resonance-pilot
COPY --from=builder /app/bin/resonance /app/resonance
COPY --from=builder /app/configs /app/configs
WORKDIR /opt/resonance/bridge
COPY pilot/bridge/package.json pilot/bridge/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts \
    && test "$(./node_modules/.bin/pi --version)" = "0.84.1"
COPY pilot/bridge/src ./src
WORKDIR /app
USER resonance
ENTRYPOINT ["/app/resonance"]
