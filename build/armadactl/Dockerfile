FROM alpine:3.10

RUN apk update && apk add --no-cache ca-certificates

RUN addgroup -S -g 2000 armada && adduser -S -u 1000 armada -G armada

USER armada

COPY ./bin/linux/armadactl /app/

WORKDIR /app

ENTRYPOINT ["./armadactl"]
