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

To build everything, run:

    docker compose -f build.yaml build

Building locally (particularly on a Raspberry Pi) is resource intensive.
Pre-built multi-arch (amd64/arm64) images are published automatically to
`ghcr.io/larsks` -- see `.github/workflows/build-images.yml`. To download the
latest images, run:

    docker compose --profile pull-only pull

Publishing runs on every push to `main` and versions releases
automatically, via [semantic-release] (config in `.releaserc.json`), from
[Conventional Commits] commit messages (`fix:` -> patch, `feat:` -> minor,
`BREAKING CHANGE:` -> major). Commits that don't follow that convention
don't bump the version or publish new images. Each release is published
under four tags -- `latest`, `x.y.z`, `x.y`, and `x` -- so you can float on
the latest release, a minor line, or a major line instead of pinning an
exact version.

[semantic-release]: https://github.com/semantic-release/semantic-release
[convential commits]: https://www.conventionalcommits.org/

## Running

First bring up the persistent services:

```sh
docker compose up -d
```

Connecting user containers can be spawned like this:

```sh
SRC_CALLSIGN=N0CALL BBS_DISABLE_ECHO=0 bash runuser.sh
```

Setting `BBS_DISABLE_ECHO=0` prevents the user container from running `stty -echo`, which is useful when connecting with a packet terminal but not so much when directly spawning a container.

If you were to use [agwwrap], you would wire things up like this if you have a local AGWPE endpoint at `127.0.0.1:8000`:

```sh
agwwrap -c MYBBS -- bash runuser.sh
```

Or with a remote AGWPE endpoint:

```sh
agwwrap -h remotehost:8010 -c MYBBS -- bash runuser.sh
```

[agwwrap]: https://github.com/larsks/agwtools

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
