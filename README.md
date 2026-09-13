# unixbbs

Packet-radio BBS built as Docker containers. See `DESIGN.md` for the full
design.

## Building the images

There are several images involved:

- Images for static services start via `compose.yaml`
- Image used for ephemeral user containers

To build everything, run the `build-all.sh` script:

    sh build-all.sh

## Running


First bring up the persistent services:

```sh
docker compose up -d
```

Connecting user containers can be spawned like this:

```sh
SRC_CALLSIGN=N0CALL sh runuser.sh
```
