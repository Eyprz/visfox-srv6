# Stand-in SRv6 headend (PCEP PCC) for the local C-Plane lab (infra/cplane).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /fakepcc .

FROM alpine:3.22
COPY --from=build /fakepcc /usr/local/bin/fakepcc
EXPOSE 8080
ENTRYPOINT ["fakepcc"]
