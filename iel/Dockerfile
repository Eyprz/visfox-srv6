# Development image for the VISFOX-SRv6 Intent-Enabled Layer (air rebuilds and restarts on source changes).
FROM golang:1.27-alpine

WORKDIR /app

RUN go install github.com/air-verse/air@latest

# Sources and modules come from the compose volumes (./iel:/app, ./api:/api for the generated
# models replaced at ../api/generated/go, and a module cache volume), so nothing else is copied here.
EXPOSE 8080

CMD ["air", "-c", ".air.toml"]
