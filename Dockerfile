FROM golang:1.17.7-alpine3.15  AS build-env

RUN echo $GOPATH

RUN apk add --no-cache git gcc musl-dev
RUN apk add --update make
WORKDIR /go/src/github.com/devtron-labs/kubewatch
ADD . /go/src/github.com/devtron-labs/kubewatch/
RUN GOOS=linux make

FROM alpine:3.9
RUN apk add --update ca-certificates
COPY --from=build-env  /go/src/github.com/devtron-labs/kubewatch .
RUN chmod +x ./kubewatch

ENTRYPOINT ["./kubewatch"]
