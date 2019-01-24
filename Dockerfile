FROM golang:1.11.4 as builder
LABEL protos="0.0.1" \
      protos.installer.metadata.name="gandi-dns" \
      protos.installer.metadata.description="DNS resource provider that uses the Gandi live API" \
      protos.installer.metadata.capabilities="ResourceProvider,InternetAccess,GetInformation" \
      protos.installer.metadata.provides="dns" \
      protos.installer.metadata.params="api_user,api_token"

WORKDIR /go/src/github.com/nustiueudinastea/gandi-dns-protos

# Force the go compiler to use modules
ENV GO111MODULE=on

COPY . .

RUN go mod download
RUN go get || true