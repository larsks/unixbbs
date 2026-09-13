#!/bin/sh

docker compose build &&
  docker build -t unixbbs-user -f container/user/Containerfile container/user
