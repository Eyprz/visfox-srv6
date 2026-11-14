# GoBGP (v4) daemon for the local C-Plane lab (infra/cplane).
FROM golang:1.27-alpine AS build
RUN go install github.com/osrg/gobgp/v4/cmd/gobgpd@latest github.com/osrg/gobgp/v4/cmd/gobgp@latest

FROM alpine:3.22
COPY --from=build /go/bin/gobgpd /go/bin/gobgp /usr/local/bin/
EXPOSE 179 50051
ENTRYPOINT ["gobgpd"]
CMD ["-f", "/etc/gobgp/gobgpd.yaml", "--api-hosts", "0.0.0.0:50051"]
