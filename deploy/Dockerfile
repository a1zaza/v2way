# Stage 1: Build frontend
FROM node:24-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

# Stage 2: Build backend with embedded frontend
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/*.go ./web/
COPY web/src/assets/fonts/ ./web/src/assets/fonts/
COPY web/fallback/ ./web/fallback/
COPY --from=frontend-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app/bin/v2way ./cmd/v2way

# Stage 3: Run the standalone gateway
FROM alpine
RUN apk add --no-cache ca-certificates tzdata tini && \
    addgroup -S -g 1000 v2way && \
    adduser -S -D -H -u 1000 -G v2way v2way && \
    mkdir -p /var/lib/v2way /www && \
    chown v2way:v2way /var/lib/v2way /www && \
    chmod 0700 /var/lib/v2way
ENV TZ=Asia/Shanghai
COPY --from=backend-builder /app/bin/v2way /usr/local/bin/v2way
COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh
WORKDIR /var/lib/v2way
USER 1000:1000
EXPOSE 4033
ENTRYPOINT ["/sbin/tini", "-g", "--", "/entrypoint.sh"]
