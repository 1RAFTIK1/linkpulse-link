# Multi-stage: собираем статический бинарник в полном Go-образе,
# в рантайм кладём только его — образ ~15 МБ и без тулчейна.
#
# Нюанс мульти-репо: пока linkpulse-contracts не опубликован, go.mod содержит
# replace на ../linkpulse-contracts — поэтому контекст сборки должен быть
# родительской папкой: docker build -f linkpulse-link/Dockerfile .
# После публикации contracts на GitHub replace уйдёт и контекст станет обычным.
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY linkpulse-contracts/ ../linkpulse-contracts/
# Слои кэша: сначала только модули (меняются редко), потом код.
COPY linkpulse-link/go.mod linkpulse-link/go.sum ./
RUN go mod download

COPY linkpulse-link/ .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/link ./cmd/link

FROM alpine:3.22

# Не root: принцип наименьших привилегий.
RUN adduser -D -u 10001 app
USER app

COPY --from=build /out/link /usr/local/bin/link
# Миграции кладём в образ: их накатывает init-контейнер/джоба, не сам сервис.
COPY --from=build /src/migrations /migrations

EXPOSE 8080
ENTRYPOINT ["link"]
