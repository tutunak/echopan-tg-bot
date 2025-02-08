FROM golang:alpine3.18 as builder
LABEL authors="tutunak"

WORKDIR /app

# Copy only necessary files
COPY go.* ./
RUN go mod download
COPY . .
# Build with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-w -s" -o echopan .

FROM alpine:3.18 as production
LABEL authors="tutunak"
COPY --from=builder /app/echopan /app/echopan
RUN addgroup -S echopan && adduser -S echopan -G echopan && \
    chown -R echopan:echopan /app
USER echopan
WORKDIR /app
CMD ["./echopan", "service"]
