FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /nyann_poker ./cmd/nyann_poker/

FROM scratch
COPY --from=build /nyann_poker /nyann_poker
ENTRYPOINT ["/nyann_poker"]
