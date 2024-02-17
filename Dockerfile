ARG GO_VERSION="1.21"
ARG RUNNER_IMAGE="alpine:3.17"

FROM golang:${GO_VERSION}-alpine

WORKDIR /app

COPY go.mod ./
RUN go mod download
COPY . .

RUN go build -o validate-gentx .

CMD ["./validate-gentx"]
