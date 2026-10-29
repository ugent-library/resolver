# Plan: slim down the container image

**Status:** not started. Split off on 2026-10-29 from the rebuild plan (`docs/archive/docker-image-build.md`, part B), which moved the image to Go 1.27.3 and Alpine 3.24 (merge `c66963c`). GitHub issue: [#2](https://github.com/ugent-library/resolver/issues/2).

## Context

resolver redirects old `lib.ugent.be` URLs to their new locations. The mapping, `urls.csv.gz` (about 8.4 million pairs), is compiled into the binary with `//go:embed`, and `loadURLS()` reads it into an in-memory map at startup. Docker Hub builds the image from `main` as `latest` through `hooks/build`, and production deploys it automatically.

The rebuild left the rest of the Dockerfile as it was. Measured on 2026-10-29:

- **The image is 248 MB, and about 60 MB of it is needed.** The final stage copies the whole build directory (`COPY --from=build /build .`), so `/dist` holds 229 MB: the 50 MB binary, plus the source, `hooks/`, `.git/` (there is no `.dockerignore`) and a second, unused 41 MB copy of `urls.csv.gz`. The ~60 MB target is the 9 MB Alpine base plus the binary.
- **The build stage runs `go get -d -v ./...`.** `-d` is deprecated and does nothing in module mode; `go build` downloads the modules itself.
- **The container runs as root** (uid 0), although resolver writes no files and listens on port 3000, above 1024.

## Changes

None of these depend on each other. They can ship in one commit or several.

- [ ] Drop `RUN go get -d -v ./...` from the build stage. To keep a cached dependency layer, use `COPY go.mod go.sum ./` and `RUN go mod download` before `COPY . .`.
- [ ] Copy only the binary into the final stage: `COPY --from=build /build/app /dist/app`.
- [ ] Add a `.dockerignore` that leaves out `.git/` and `docs/`. Do not leave out `urls.csv.gz`: `go build` needs it in the build context for the embed.
- [ ] Run the container as a non-root user (`USER nobody`, or a dedicated user). Check the Nomad job first for anything that assumes root.

## Check that every URL still loads

`urls.csv.gz` reaches the image only inside the binary. A build stage that misses the file fails `go build`, and a broken file makes the container panic on start, so the risk is a resolver that starts with fewer URLs than it should. Run these checks after every change to the Dockerfile or `.dockerignore`, and whenever `urls.csv.gz` is replaced. Build with `DOCKERFILE_PATH=Dockerfile IMAGE_NAME=resolver:local hooks/build`, so the image is built the way Docker Hub builds it.

- [ ] Work out the expected count. Some source URLs appear more than once (on 2026-10-29: 8,533,368 rows, 8,357,913 distinct sources), and a later row overwrites an earlier one in the map, so the expected count is the number of *distinct* sources, not the line count. The file has no quoted fields, so `cut` is enough:

  ```sh
  gzip -dc urls.csv.gz | cut -d, -f1 | sort -u | wc -l
  ```

- [ ] Start the container and read the startup log. It must contain `loaded N urls` with exactly that N, followed by `server listening`:

  ```sh
  docker run --rm -d --name resolver-check -p 3000:3000 resolver:local
  docker logs -f resolver-check   # wait for "server listening", then Ctrl-C
  ```

- [ ] Check that a known URL redirects to its destination. Take it from the last line of the file, since the last occurrence of a source is the one the map keeps:

  ```sh
  row="$(gzip -dc urls.csv.gz | tail -1)"
  src="${row%%,*}"; dst="${row#*,}"
  curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' --get --data-urlencode "url=$src" http://localhost:3000/
  echo "expected: 301 $dst"
  ```

- [ ] Check that an unknown URL gives `404`, that `GET /status` gives `{"status":"up"}`, and that the log has no errors or panics. Then stop the container: `docker stop resolver-check`.
- [ ] Check the image size: `docker image inspect resolver:local --format '{{.Size}}'` should be around 60 MB.
- [ ] After the merge, deploy and check that production logs `loaded N urls` with the same N.

## Left over from the rebuild plan

- [ ] **Periodic rebuilds.** Docker Hub builds only on a push to `main` or a manual trigger. `SNAPSHOT_EPOCH` makes a build that runs pick up new Alpine packages, but it does not make a build happen, so if resolver goes months without a push its image keeps old packages. Decide how rebuilds happen between code changes: by hand after each Go point release and Alpine security update, or on a schedule from outside Docker Hub. Bumping Go means changing the `golang:1.27.3-alpine` tag and the `go` directive in go.mod.
- [ ] **Document the image build** in the README: Docker Hub builds `main` as `latest` and production deploys it automatically; `hooks/build` passes `--pull` and `SNAPSHOT_EPOCH`; both base images are pinned (`golang:1.27.3-alpine`, `alpine:3.24`); how rebuilds are triggered. Include the slimmed Dockerfile from this plan.

## Retiring the plan

- [ ] Move this plan to `docs/archive/` once the image is slimmed, the checks pass in production and the two leftovers are done.
