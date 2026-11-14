# Stand-in BGP-LS speaker for the local C-Plane lab (infra/cplane): adds a TED file to GoBGP's RIB.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /fakebgpls .

FROM alpine:3.22
COPY --from=build /fakebgpls /usr/local/bin/fakebgpls
ENTRYPOINT ["fakebgpls"]
