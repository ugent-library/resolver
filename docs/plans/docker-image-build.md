# Plan: rebuild the container image with current Go and CA certificates

**Status:** copied from ingest's archived plan (`ingest/docs/archive/docker-image-build.md`) on 2026-10-26. Part A not started.

## Reusing this plan in another project

This plan is written to be copied to other Go projects that build an Alpine image on Docker Hub and run on Nomad. Everything outside *This project* and *Part B* applies to such a project as it stands. To reuse it:

1. Copy this file to the other project's `docs/plans/`.
2. Fill in *This project* with that project's values. The steps in part A refer to them by name (the binary path, the build and test commands).
3. Replace *Part B* with that project's own cleanup, or delete it.
4. Work through part A there and record the answers to the open questions in that copy.

There is no need to repeat the test in *What we tested*: it shows how Go's `crypto/x509` and Alpine's CA bundle behave, which is the same for every Go binary on Alpine.

## This project

| | resolver |
| --- | --- |
| Outbound HTTPS calls | none. resolver only answers `GET /status` and redirects `GET /?url=` using the embedded `urls.csv.gz` |
| Build stage today | `FROM golang:alpine`, runs `go get -d -v ./...` then `go build -o app -v` |
| Final stage today | `FROM alpine:latest`, no package upgrade; copies the whole `/build` directory (source included) to `/dist` |
| Binary in the image | `/dist/app` |
| `go` directive in go.mod | `go 1.24.4` |
| `hooks/build` | exists; passes `SOURCE_BRANCH`, `SOURCE_COMMIT`, `IMAGE_NAME`; no `--pull`, no `SNAPSHOT_EPOCH` |
| Build and test commands | `go build -o app`, `go vet ./...` (there are no tests and no Makefile) |
| Running containers | *to look up* (number of Nomad allocations) |
| Branches | only `main` |
| Merge commit message | *to decide*; the history has no merge commits yet |
| Releases | none; no git tags |
| Docker Hub build rules | *to look up* (which tag `main` builds, which tag development and production run) |

The Dockerfile declares `ARG SOURCE_BRANCH`, `SOURCE_COMMIT` and `IMAGE_NAME` and copies them into `ENV`, and the existing `hooks/build` fills them in. No code in resolver reads them; they only tell someone inspecting a container which commit it runs.

**Exposure.** Because resolver makes no outbound TLS connections, the eMudhra compromise in *Why now* does not affect it: it never verifies a server certificate. The plan is still worth doing so the image stops depending on floating tags and stale packages. It is not urgent, and Go 1.24 no longer gets security fixes, so moving to a supported Go release is the main reason to do it.

## Why now

On 17 October 2026 a researcher reported that the private keys of eight eMudhra intermediate CA certificates could be recovered from their public certificates. Anyone holding those keys can issue a TLS certificate that a client trusts for any domain. Browsers blocked the intermediates between 17 and 20 October, and the root programs distrusted `emSign ECC Root CA - G3` on 21 October. A Go service is not a browser: every outbound HTTPS call trusts whatever Go's `crypto/x509` and the image's CA bundle trust. Go does not check revocation, so the revocation of the intermediates on 18 and 19 October does not protect us.

The [disclosure site](https://emudhra.we-have-a-problem.com/) publishes the affected certificates in `impacted-intermediates.json`: sixteen certificates for the eight keys. Each intermediate exists twice, once issued by an emSign root and once cross-signed by Sectigo's `AAA Certificate Services`, and both copies carry the same key.

Two upstream fixes exist, and each one closes the hole in an Alpine image on its own:

- **Go 1.27.3** (released 2026-10-19, [CL 851047](https://go-review.googlesource.com/c/go/+/851047), CVE-2026-111943) makes `x509.Certificate.Verify` drop chains through the compromised intermediates, identified by the SHA-256 hash of their public key. That covers both copies of every intermediate, whatever root the chain ends in. The fix is compiled into the binary. Go 1.26.10 carries the same fix for projects still on Go 1.26.
- **Alpine `ca-certificates-bundle 20261020-r0`** (v3.24, v3.23, v3.22 and edge) removes `emSign ECC Root CA - G3`, following Mozilla's NSS 3.131.1.

### What we tested (2026-10-26)

A small Go HTTPS client, built once with Go 1.27.2 and once with Go 1.27.3, run in Alpine images with the old (`20260909-r0`) and the new (`20261020-r0`) bundle, against the four test hosts. The bundle contents were checked by SHA-256 fingerprint:

| Bundle | `emSign ECC Root CA - G3` | `emSign Root TLS CA - G3` | `AAA Certificate Services` |
| --- | --- | --- | --- |
| `20260909-r0` | present | absent | absent |
| `20261020-r0` | absent | absent | absent |

| Go | Bundle | emSign chain | Sectigo chain |
| --- | --- | --- | --- |
| 1.27.2 | old | **accepted** | rejected |
| 1.27.3 | old | rejected (`no valid chains built`) | rejected |
| 1.27.2 | new | rejected (`certificate signed by unknown authority`) | rejected |
| 1.27.3 | new | rejected | rejected |

The hosts that serve certificates never logged to Certificate Transparency gave the same results, since Go does not enforce CT. So:

- An image is exposed only with an old Go and an old bundle together. Any image built before 20 October most likely has both.
- The Sectigo cross-signature does not reach an Alpine image: Alpine's bundle, built from Mozilla's, does not contain `AAA Certificate Services`. It matters for trust stores that do contain it, such as those of macOS and Windows, which is one reason the Go fix is worth having alongside the bundle.
- The error text shows which fix rejected a chain: `no valid chains built` is Go's filter, `certificate signed by unknown authority` is the bundle.

We take both fixes, so that a later mistake in one (a stale bundle, a toolchain left behind) does not reopen the hole. Because each fix was shown to work, the checks below only confirm the versions: Go 1.27.3 in the binary and bundle `20261020-r0` or later in the image.

## Why a rebuild may pick up neither

- `FROM golang:alpine` and `FROM alpine:latest` are floating tags. Docker uses whatever copy of the tag it already has unless the build runs with `--pull`, so a rebuild can produce a binary built with an older Go, on an old bundle.
- Without a package upgrade in the final stage, the image has whatever bundle its base image shipped with. And a `RUN apk upgrade` alone does not help for long: Docker reuses a `RUN` layer as long as its parent layer and its command text are unchanged; it never looks at the Alpine package index. The layer would run once and stay cached.
- Docker Hub runs `hooks/build` instead of its own `docker build` when that file exists. Its default build does not pass `--build-arg`, so any `ARG` in the Dockerfile stays empty.

**Model:** `ugent-library/depot-management` solves the caching problem. Its `hooks/build` passes `--build-arg SNAPSHOT_EPOCH="$(date +%s)"`, and its Dockerfile declares `ARG SNAPSHOT_EPOCH` right before the package-install `RUN`. A declared `ARG` is part of the environment of every `RUN` after it in the same stage, so a new value on every build makes that `RUN` miss the cache and run again.

## Open questions

- **Periodic rebuilds.** Docker Hub builds only on a push or a manual trigger. `SNAPSHOT_EPOCH` makes a build that runs pick up new packages; it does not make a build happen. If a project goes months without a push, its image keeps an old Go and an old bundle. Decide how rebuilds happen between code changes: by hand after each Go point release and Alpine security update, or on a schedule from outside Docker Hub. Check what Docker Hub currently offers for triggering builds before choosing. *Ingest chose by hand. Resolver: open.*
- **Raise the `go` directive in go.mod?** Raising it to `go 1.27.3` makes a build on an older toolchain fail outright, because the official golang images set `GOTOOLCHAIN=local`. It also forces every developer onto 1.27.3 (Go downloads it automatically unless `GOTOOLCHAIN=local` is set locally). Pinning the image tag already fixes the Docker build; this would add a check that stops a stale toolchain anywhere. *Ingest raised it. Resolver: open. Going from 1.24.4 to 1.27.3 skips three minor releases, so also run `go get -u ./...` and `go mod tidy` and check that the dependencies still build.*
- **Docker Hub "Build caching" setting.** Look up whether it is on for the repository (Builds, Configure automated builds). If it is off, every build already starts from scratch and the EPOCH argument only matters if caching is turned on later. Keep the argument either way, so the Dockerfile does not depend on that setting. *Resolver: to look up.*

## Part A: pick up both fixes

The fix branches from `main`, not `dev`, so that releasing it does not also release whatever is waiting on `dev`. It is merged into both, so `dev` does not lose it at the next release. *Resolver has no `dev` branch: skip the merge in A5 and test the image built from the feature branch, or whatever the Docker Hub build rules allow, before merging into `main`.*

### A0. Branch

- [ ] Switch to `main` and bring it up to date: `git switch main && git pull --ff-only`.
- [ ] Create the feature branch: `git switch -c docker-image-build`. Steps A1 to A3 are commits on this branch.

### A1. Go

- [ ] Change the build stage to `FROM golang:1.27.3-alpine`.
- [ ] Decide the go.mod question above; if yes, change the `go` directive to `1.27.3` in the same commit.
- [ ] Build locally with `docker build --pull` and check which toolchain built the binary: copy it out with `docker create` and `docker cp <container>:<binary> ./app`, where `<binary>` is the path from *This project*, then run `go version ./app` on the host. It must print `go1.27.3`.

### A2. CA bundle, kept current

- [ ] Change the final stage to `FROM alpine:3.24`, the release `alpine:latest` points to today. Pinning it makes moving to a new Alpine release a deliberate change rather than something a rebuild does unnoticed.
- [ ] Right after that `FROM`, add the argument and the upgrade. The `ARG` must sit inside the final stage; one declared before the first `FROM` is not visible there. Upgrade all packages rather than naming one: the base image ships `ca-certificates-bundle`, not `ca-certificates`, so `apk upgrade ca-certificates` would change nothing, and the other packages get their security fixes too.

  ```dockerfile
  FROM alpine:3.24
  # hooks/build passes a new value on every build, so this layer is never served from cache.
  ARG SNAPSHOT_EPOCH
  RUN apk upgrade --no-cache
  ```

- [ ] Check the result: `docker run --rm --entrypoint apk <image> list -I ca-certificates-bundle` shows `ca-certificates-bundle-20261020-r0` or later. Grepping the bundle for "emSign" proves nothing: `/etc/ssl/certs/ca-certificates.crt` holds PEM blocks only, without the root names, so the count is always 0.

### A3. Docker Hub build hook

- [ ] Add `hooks/build`, modelled on depot-management's. It fills in `SOURCE_BRANCH` and `SOURCE_COMMIT` from git when Docker Hub leaves them empty. Leave out the `--build-arg` lines for arguments the Dockerfile does not declare.

  ```bash
  #!/bin/bash
  if [[ -z "$SOURCE_BRANCH" ]]; then
  	export SOURCE_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
  fi
  if [[ -z "$SOURCE_COMMIT" ]]; then
  	export SOURCE_COMMIT="$(git rev-parse HEAD)"
  fi

  echo "SOURCE_BRANCH: $SOURCE_BRANCH"
  echo "SOURCE_COMMIT: $SOURCE_COMMIT"
  echo "IMAGE_NAME: $IMAGE_NAME"

  docker build \
      --pull \
      --build-arg SOURCE_BRANCH=$SOURCE_BRANCH \
      --build-arg SOURCE_COMMIT=$SOURCE_COMMIT \
      --build-arg IMAGE_NAME=$IMAGE_NAME \
      --build-arg SNAPSHOT_EPOCH="$(date +%s)" \
      -f $DOCKERFILE_PATH \
      -t $IMAGE_NAME .
  ```

- [ ] Make it executable (`chmod +x hooks/build`) and commit the mode.
- [ ] Run the hook locally with `DOCKERFILE_PATH=Dockerfile IMAGE_NAME=<project>:local hooks/build` to check it works outside Docker Hub.
- [ ] Look up the Docker Hub "Build caching" setting and record the answer in this plan.

### A4. Check locally

- [ ] Run the build and test commands from *This project* with the Go 1.27.3 toolchain.
- [ ] Look up the Docker Hub build rules and record them in *This project*. Steps A5 and A6 depend on them.

### A5. Merge into dev and test there

- [ ] Merge the feature branch into `dev` with a merge commit: `git switch dev && git pull --ff-only && git merge --no-ff docker-image-build`, with the merge commit message from *This project*.
- [ ] Push `dev`. Docker Hub builds from what is pushed, so the development environment can only be tested after this push.
- [ ] In the Docker Hub build log, check that the hook ran (it prints `SOURCE_BRANCH`, `SOURCE_COMMIT` and `IMAGE_NAME`) and that the `apk upgrade` step ran rather than showing `CACHED`.
- [ ] Pull the image Docker Hub built and repeat the checks from A1 and A2: `go version` on the binary and the bundle version. Also check that `env` shows non-empty `SOURCE_BRANCH` and `SOURCE_COMMIT`.
- [ ] Deploy to the development environment. Open a shell in every running allocation and run the command below. It uses resolver's binary path, `/dist/app`. Type the path itself: a placeholder such as `<binary>` makes the shell read `<` as a redirect and fail with a syntax error.

  ```sh
  grep -a -o -E -m1 "go1\.[0-9]+\.[0-9]+" /dist/app | head -1; apk list -I ca-certificates-bundle
  ```

  It must print `go1.27.3` and `ca-certificates-bundle-20261020-r0` or later. The image has no Go toolchain, but the binary holds its Go version as text, which BusyBox `grep` can read.
- [ ] Check that the services work as before on the development environment.
- [ ] Trigger a second Docker Hub build of `dev` without code changes and check that the `apk upgrade` step runs again. Skip this if Build caching is off.

### A6. Merge into main and release

- [ ] Merge the feature branch into `main` with a merge commit: `git switch main && git pull --ff-only && git merge --no-ff docker-image-build`, with the merge commit message from *This project*. Merge the feature branch, not `dev`, so nothing else from `dev` comes along.
- [ ] Push `main`.
- [ ] Decide whether a release is needed. It is if production runs an image built from a tag: a new tag is then the only way to get the new Dockerfile into production, because rebuilding the old tag rebuilds the old Dockerfile. If production runs the image built from `main`, the push is enough. *Resolver has no tags yet; check what production runs.*
- [ ] If a release is needed: create the next patch version as an annotated tag on the merge commit on `main` (resolver has no tags yet, so `v0.1.0` or `v1.0.0`), push the tag, and write a GitHub release that says the image is rebuilt with Go 1.27.3 and the updated CA bundle to move off unsupported Go 1.24 and pick up current Alpine packages, with no other changes and no upgrade steps.
- [ ] Deploy to production and run the same check in every running allocation as in A5.
- [ ] Delete the feature branch, locally and on GitHub if it was pushed.
- [ ] Decide the periodic rebuild question and record the answer here.

## Part B: Dockerfile cleanup (resolver only)

None of this is needed for part A. It can ship after it, in its own commits.

- [ ] Drop `RUN go get -d -v ./...` from the build stage. `-d` is deprecated and does nothing in module mode; `go build` downloads the modules itself. To keep a cached dependency layer, use `COPY go.mod go.sum ./` and `RUN go mod download` before `COPY . .`.
- [ ] Copy only the binary into the final stage: `COPY --from=build /build/app /dist/app`. Today `COPY --from=build /build .` also ships the source, `go.mod`, `hooks/` and `urls.csv.gz`, which the binary already embeds.
- [ ] Add a `.dockerignore` that leaves out `.git/` and `docs/`.
- [ ] Run the container as a non-root user (`USER nobody`, or a dedicated user). resolver writes no files and listens on port 3000, above 1024, so nothing should depend on root. Check the deployment configuration first.
- [ ] Check that the slimmed image still loads every URL. `urls.csv.gz` reaches the image only inside the binary (`//go:embed`), and `loadURLS()` reads it into the `urls` map at startup, so a build stage that misses the file fails `go build`, and a bad file makes the container panic on start. Run these checks after every change to the Dockerfile or `.dockerignore`, and whenever `urls.csv.gz` is replaced:

  1. Work out the expected count. Some source URLs appear more than once (on 2026-10-29: 8,533,368 rows, 8,357,913 distinct sources), and a later row overwrites an earlier one in the map, so the expected count is the number of *distinct* sources, not the line count. The file has no quoted fields, so `cut` is enough:

     ```sh
     gzip -dc urls.csv.gz | cut -d, -f1 | sort -u | wc -l
     ```

  2. Start the container and read the startup log. It must contain `loaded N urls` with exactly that N, followed by `server listening`:

     ```sh
     docker run --rm -d --name resolver-check -p 3000:3000 <image>
     docker logs -f resolver-check   # wait for "server listening", then Ctrl-C
     ```

  3. Check that a known URL redirects to its destination. Take it from the last line of the file, since the last occurrence of a source is the one the map keeps:

     ```sh
     row="$(gzip -dc urls.csv.gz | tail -1)"
     src="${row%%,*}"; dst="${row#*,}"
     curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' --get --data-urlencode "url=$src" http://localhost:3000/
     echo "expected: 301 $dst"
     ```

  4. Check that an unknown URL gives `404`, and that `GET /status` gives `{"status":"up"}`. Then stop the container: `docker stop resolver-check`.

## Retiring the plan

- [ ] Describe how the image is built (Docker Hub, `hooks/build`, the `SNAPSHOT_EPOCH` argument, pinned base images, how rebuilds are triggered) in the project's README or design doc, so the next person to bump Go or Alpine knows which lines to change.
- [ ] Move this plan to `docs/archive/` or delete it.
