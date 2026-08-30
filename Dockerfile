# docket in a container.
#
# The vault is not in the image. It is a git repository that belongs to you, and
# baking it in would make the image the source of truth instead of the
# repository — the opposite of the whole design. Mount it:
#
#   docker run --rm -p 8080:8080 -v "$PWD:/vault" ghcr.io/vadymdidenkolab/docket
#
# Two stages: build with the Go toolchain, run on Alpine. Not scratch, because
# the server shells out to git for every write and needs certificates to ask a
# git host who you are.

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so editing the code does not re-download them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION is stamped in by the release workflow. An untagged build reports what
# Go's build info says, which is the honest answer for one.
ARG VERSION=""
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/vadymdidenkolab/docket/internal/cli.version=${VERSION}" \
    -o /out/docket ./cmd/docket

FROM alpine:3.22

RUN apk add --no-cache git ca-certificates tini

# A mounted vault is owned by whoever owns it on the host, which is almost never
# this container's user. Without this, git refuses to touch it — "dubious
# ownership" — and every write fails with a message about a repository the
# person is looking at right now. The container's only job is that repository.
RUN git config --system --add safe.directory '*'

# Not root. The vault is mounted from outside and the process only needs to read
# and write what the mount already allows.
RUN adduser -D -u 10001 docket
USER docket

COPY --from=build /out/docket /usr/local/bin/docket

WORKDIR /vault
EXPOSE 8080

# tini reaps the git processes the server starts, and passes signals through, so
# docker stop stops rather than waits.
ENTRYPOINT ["/sbin/tini", "--", "docket"]

# Listening on 0.0.0.0 because in a container the loopback default is a server
# nobody can reach. Publishing the port is the decision to expose it.
CMD ["serve", "--addr", "0.0.0.0:8080"]
