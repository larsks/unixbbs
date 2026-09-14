#!/bin/bash

compose_args=()
compose_build_args=()

if grep -q 'Raspberry Pi' /proc/cpuinfo; then
  echo "Activating raspberry pi mitigations"
  export BUILDX_BAKE_MAX_PARALLEL=1
  export GOMAXPROCS=1
  compose_args+=(--progress=plain)
  compose_build_args=(--parallel=false)
fi

docker compose -f build.yaml "${compose_args[@]}" build "${compose_build_args[@]}" &&
