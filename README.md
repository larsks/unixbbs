# unixbbs

Packet-radio BBS built as Docker containers. See `DESIGN.md` for the full
design.

## Building the images

There are several images involved:

- Images for static services run via `compose.yaml`, and build via
  `build.yaml` (build configuration -- including a shared `base` image
  the others build from -- is kept in a separate file so that plain
  `docker compose up`/`pull` never has to know about it; see
  `compose.yaml`'s header comment)
- Image used for ephemeral user containers

To build everything, run the `build-all.sh` script:

    sh build-all.sh

which runs `docker compose -f build.yaml build`, then a plain `docker
build` for the ephemeral user container.

Building locally (particularly on a Raspberry Pi) is resource intensive.
Pre-built multi-arch (amd64/arm64) images are published automatically to
`ghcr.io/larsks` -- see `.github/workflows/build-images.yml`. To use them
instead of building locally, run `docker compose pull` (compose.yaml
declares an `image:` for each service); see `.env.example` for pinning a
specific `TAG` instead of always tracking `latest`.

Publishing runs on every push to `main` and versions releases
automatically, via [semantic-release](https://github.com/semantic-release/semantic-release)
(config in `.releaserc.json`), from [Conventional Commits](https://www.conventionalcommits.org/)
commit messages (`fix:` -> patch, `feat:` -> minor, `BREAKING CHANGE:` ->
major). Commits that don't follow that convention don't bump the version
or publish new images. Each release is published under four tags --
`latest`, `x.y.z`, `x.y`, and `x` -- so you can float on the latest
release, a minor line, or a major line instead of pinning an exact
version.

## Running


First bring up the persistent services:

```sh
docker compose up -d
```

Connecting user containers can be spawned like this:

```sh
SRC_CALLSIGN=N0CALL sh runuser.sh
```

## License

unixbbs -- a simple unix bbs\
Copyright (C) 2026 Lars Kellogg-Stedman

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
