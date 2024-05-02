FROM golang:1.20-alpine3.18  AS build-env

RUN echo $GOPATH

RUN apk add --no-cache git gcc musl-dev
RUN apk add --update make
WORKDIR /go/src/github.com/devtron-labs/kubewatch
ADD . /go/src/github.com/devtron-labs/kubewatch
RUN ls -l
RUN GOOS=linux make

RUN ls -l

FROM alpine:3.18

RUN apk add --update ca-certificates

RUN adduser -D devtron

COPY --from=build-env  /go/src/github.com/devtron-labs/kubewatch .

RUN chown devtron:devtron ./kubewatch

RUN chmod +x ./kubewatch

USER devtron

# ENTRYPOINT ["tail", "-f", "/dev/null"]

ENTRYPOINT ["./kubewatch/kubewatch"]
