FROM golang:alpine3.18 as builder
LABEL authors="tutunak"

COPY . /app
WORKDIR /app
RUN go build -o echopan .

FROM alpine:3.18 as production
LABEL authors="tutunak"
COPY --from=builder /app/echopan /app/echopan
RUN addgroup -S echopan && adduser -S echopan -G echopan && \
    chown -R echopan:echopan /app
USER echopan
WORKDIR /app
CMD ["./echopan", "service"]
