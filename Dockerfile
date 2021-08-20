FROM golang:1.14.13-alpine3.12  AS build-env

RUN echo $GOPATH

RUN apk add --no-cache git gcc musl-dev
RUN apk add --update make
WORKDIR /go/src/github.com/devtron-labs/kubewatch
ADD . /go/src/github.com/devtron-labs/kubewatch/
RUN GOOS=linux make

FROM alpine:3.4
RUN apk add --update ca-certificates
COPY --from=build-env  /go/src/github.com/devtron-labs/kubewatch .
RUN chmod +x ./kubewatch

ENTRYPOINT ["./kubewatch"]
